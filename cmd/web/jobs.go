package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leotaku/kojirou/cmd/job"
	md "github.com/leotaku/kojirou/mangadex"
)

const maxKeptJobs = 50

// Runner runs one download job; job.Run in production.
type Runner func(ctx context.Context, opts job.Options, rep job.Reporter) (*job.Result, error)

type StepInfo struct {
	Title     string `json:"title"`
	Current   int64  `json:"current"`
	Total     int64  `json:"total"`
	Status    string `json:"status"` // running, done, skipped, error
	Message   string `json:"message,omitempty"`
	Vanishing bool   `json:"vanishing"`
}

type JobInfo struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Source   string     `json:"source"`
	Format   string     `json:"format"`
	State    string     `json:"state"` // queued, running, done, failed, canceled
	Error    string     `json:"error,omitempty"`
	Chapters int        `json:"chapters"`
	Dir      string     `json:"dir,omitempty"`
	Created  time.Time  `json:"created"`
	Finished *time.Time `json:"finished,omitempty"`
	Steps    []StepInfo `json:"steps"`
}

type step struct {
	title     string
	vanishing bool
	current   atomic.Int64
	total     atomic.Int64
	mu        sync.Mutex
	status    string
	message   string
}

func (s *step) set(status, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.message = status, message
}

func (s *step) info() StepInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	return StepInfo{
		Title:     s.title,
		Current:   s.current.Load(),
		Total:     s.total.Load(),
		Status:    s.status,
		Message:   s.message,
		Vanishing: s.vanishing,
	}
}

type stepProgress struct{ s *step }

func (p stepProgress) Increase(n int)                       { p.s.total.Add(int64(n)) }
func (p stepProgress) Add(n int)                            { p.s.current.Add(int64(n)) }
func (p stepProgress) NewProxyWriter(w io.Writer) io.Writer { return w }
func (p stepProgress) Done()                                { p.s.set("done", "") }
func (p stepProgress) Cancel(message string) {
	if message == "Skipped" {
		p.s.set("skipped", message)
	} else {
		p.s.set("error", message)
	}
}

type Job struct {
	mu       sync.Mutex
	info     JobInfo
	steps    []*step
	opts     job.Options
	cancel   context.CancelFunc
	canceled bool
}

func (j *Job) snapshot() JobInfo {
	j.mu.Lock()
	defer j.mu.Unlock()
	info := j.info
	info.Steps = make([]StepInfo, 0, len(j.steps))
	for _, s := range j.steps {
		info.Steps = append(info.Steps, s.info())
	}

	return info
}

// Summary and Task implement job.Reporter.
func (j *Job) Summary(m *md.Manga) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.info.Title = m.Info.Title
	j.info.Chapters = len(m.Chapters())
}

func (j *Job) Task(title string, vanishing bool) job.Progress {
	s := &step{title: title, vanishing: vanishing, status: "running"}
	j.mu.Lock()
	j.steps = append(j.steps, s)
	j.mu.Unlock()

	return stepProgress{s}
}

type Manager struct {
	run   Runner
	queue chan *Job

	mu   sync.Mutex
	jobs []*Job
	next int
}

// NewManager starts a worker that runs queued jobs one at a time until ctx ends.
func NewManager(ctx context.Context, run Runner) *Manager {
	m := &Manager{run: run, queue: make(chan *Job, 256)}
	go m.work(ctx)

	return m
}

var errQueueFull = errors.New("too many queued downloads")

func (m *Manager) Enqueue(opts job.Options, title, source string) (JobInfo, error) {
	m.mu.Lock()
	m.next++
	j := &Job{opts: opts, info: JobInfo{
		ID:      fmt.Sprintf("j%d", m.next),
		Title:   title,
		Source:  source,
		Format:  string(opts.Format),
		State:   "queued",
		Created: time.Now(),
	}}
	m.jobs = append(m.jobs, j)
	m.trim()
	m.mu.Unlock()

	select {
	case m.queue <- j:
		return j.snapshot(), nil
	default:
		j.mu.Lock()
		j.info.State, j.info.Error = "failed", errQueueFull.Error()
		j.mu.Unlock()
		return j.snapshot(), errQueueFull
	}
}

// trim drops the oldest finished jobs. Callers hold m.mu.
func (m *Manager) trim() {
	for len(m.jobs) > maxKeptJobs {
		dropped := false
		for i, j := range m.jobs {
			j.mu.Lock()
			state := j.info.State
			j.mu.Unlock()
			if state == "done" || state == "failed" || state == "canceled" {
				m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			return
		}
	}
}

func (m *Manager) List() []JobInfo {
	m.mu.Lock()
	jobs := append([]*Job(nil), m.jobs...)
	m.mu.Unlock()

	list := make([]JobInfo, 0, len(jobs))
	for i := len(jobs) - 1; i >= 0; i-- { // newest first
		list = append(list, jobs[i].snapshot())
	}

	return list
}

func (m *Manager) find(id string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.info.ID == id {
			return j
		}
	}

	return nil
}

// Cancel stops a queued or running job. It reports whether the job exists.
func (m *Manager) Cancel(id string) bool {
	j := m.find(id)
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	switch j.info.State {
	case "queued":
		j.canceled = true
		j.info.State = "canceled"
		now := time.Now()
		j.info.Finished = &now
	case "running":
		j.canceled = true
		if j.cancel != nil {
			j.cancel()
		}
	}

	return true
}

func (m *Manager) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-m.queue:
			m.runJob(ctx, j)
		}
	}
}

func (m *Manager) runJob(parent context.Context, j *Job) {
	j.mu.Lock()
	if j.canceled {
		j.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	j.cancel = cancel
	j.info.State = "running"
	j.mu.Unlock()
	defer cancel()

	res, err := m.run(ctx, j.opts, j)

	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.info.Finished = &now
	if res != nil {
		j.info.Dir = res.Dir
		if res.Title != "" {
			j.info.Title = res.Title
		}
	}
	switch {
	case j.canceled || errors.Is(err, context.Canceled):
		j.info.State = "canceled"
	case err != nil:
		j.info.State, j.info.Error = "failed", err.Error()
	default:
		j.info.State = "done"
	}
}
