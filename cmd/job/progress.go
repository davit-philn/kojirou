package job

import "io"

type nopProgress struct{}

func (nopProgress) Increase(int)                         {}
func (nopProgress) Add(int)                              {}
func (nopProgress) NewProxyWriter(w io.Writer) io.Writer { return w }
func (nopProgress) Done()                                {}
func (nopProgress) Cancel(string)                        {}
