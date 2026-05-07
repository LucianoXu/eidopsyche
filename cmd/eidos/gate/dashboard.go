package gate

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

var dashboardNoOpen bool

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Open the gate daemon's web dashboard in the default browser",
	Long: `Probes the running daemon's dashboard URL, prints it, and (unless
--no-open) opens the OS default browser. The dashboard is loopback-only;
for remote access use SSH port-forwarding.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		stateDir, err := config.ResolveStateDir(globalStateDir)
		if err != nil {
			return err
		}
		cfg, err := config.Load(filepath.Join(stateDir, "config.toml"))
		if err != nil {
			return err
		}
		if !cfg.Dashboard.Enabled {
			return fmt.Errorf("dashboard is disabled in config (%s/config.toml [dashboard].enabled)", stateDir)
		}
		url := "http://" + cfg.Dashboard.Listen

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", url+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("dashboard not reachable at %s; is the daemon running? (%w)", url, err)
		}
		_ = resp.Body.Close()

		fmt.Println(url)
		if dashboardNoOpen {
			return nil
		}
		return openBrowser(url)
	},
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("auto-open not supported on %s; open %s manually", runtime.GOOS, url)
	}
	return cmd.Start()
}

func init() {
	dashboardCmd.Flags().BoolVar(&dashboardNoOpen, "no-open", false, "print URL only; do not auto-launch browser")
	rootCmd.AddCommand(dashboardCmd)
}
