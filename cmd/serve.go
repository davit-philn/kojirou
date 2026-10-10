package cmd

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/leotaku/kojirou/cmd/formats/download"
	"github.com/leotaku/kojirou/cmd/web"
	md "github.com/leotaku/kojirou/mangadex"
	"github.com/spf13/cobra"
)

var (
	serveAddr      string
	serveLibrary   string
	serveConfigDir string
	serveNoOpen    bool
	serveAPIURL    string
	serveCoversURL string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web interface for browsing and downloading series",
	Long: `Start a local web interface.

Browse or search MangaDex, pick volumes and chapters, download them and
find the results in the library folder. Sources described by CSS selectors
can be added in the browser.

The server has no login and is meant for your own computer: it only listens
on the local machine unless you pass another address with --addr.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return serve(cmd)
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:8080", "address to listen on")
	serveCmd.Flags().StringVar(&serveLibrary, "library", "library", "folder where downloads are stored")
	serveCmd.Flags().StringVar(&serveConfigDir, "config-dir", "", "folder for saved sources (default: user config directory)")
	serveCmd.Flags().BoolVar(&serveNoOpen, "no-open", false, "do not open the browser")
	serveCmd.Flags().StringVar(&serveAPIURL, "api-url", "", "MangaDex API base URL")
	serveCmd.Flags().StringVar(&serveCoversURL, "covers-url", "", "MangaDex covers base URL")
	_ = serveCmd.Flags().MarkHidden("api-url")
	_ = serveCmd.Flags().MarkHidden("covers-url")
	rootCmd.AddCommand(serveCmd)
}

func serve(cmd *cobra.Command) error {
	configDir := serveConfigDir
	if configDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = "."
		}
		configDir = filepath.Join(base, "kojirou")
	}

	dex := md.NewClient()
	if serveAPIURL != "" || serveCoversURL != "" {
		apiURL, err := url.Parse(orDefault(serveAPIURL, "https://api.mangadex.org/"))
		if err != nil {
			return fmt.Errorf("--api-url: %w", err)
		}
		coversURL, err := url.Parse(orDefault(serveCoversURL, md.CoverBaseURL.String()))
		if err != nil {
			return fmt.Errorf("--covers-url: %w", err)
		}
		dex = dex.WithBaseURLs(*apiURL, *coversURL)
	}
	download.SetMangadexClient(dex)

	ctx := cmd.Context()
	srv, err := web.New(ctx, web.Config{
		Addr:       serveAddr,
		LibraryDir: serveLibrary,
		ConfigDir:  configDir,
		Version:    buildInfo.Main.Version,
		Dex:        dex,
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", serveAddr)
	if err != nil {
		return fmt.Errorf("listen on %v: %w", serveAddr, err)
	}

	address := "http://" + ln.Addr().String() + "/"
	fmt.Printf("Kojirou is running at %v\nDownloads are saved in %v\nPress Ctrl+C to stop.\n", address, serveLibrary)
	if host, _, err := net.SplitHostPort(serveAddr); err == nil && !isLoopback(host) {
		fmt.Println("Warning: this address is reachable from other computers and has no login.")
	}
	if !serveNoOpen {
		openBrowser(address)
	}

	return srv.Serve(ctx, ln)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

func openBrowser(address string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		c = exec.Command("open", address)
	default:
		c = exec.Command("xdg-open", address)
	}
	_ = c.Start() // best effort: the address is printed anyway
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
