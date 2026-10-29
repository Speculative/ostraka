package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Speculative/ostraka/internal/store"
	"github.com/Speculative/ostraka/internal/supervisor"
	"github.com/Speculative/ostraka/internal/tui"
	"github.com/Speculative/ostraka/internal/webui"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{Use: "web", Short: "Serve the browser and desktop web workspace", Args: cobra.NoArgs, RunE: runWeb}
	cmd.Flags().String("listen", "127.0.0.1:8090", "HTTP listen address (use a trusted network only)")
	cmd.Flags().Bool("supervise", false, "Own the agent supervisor (requires the TUI to be closed)")
	rootCmd.AddCommand(cmd)
}
func runWeb(cmd *cobra.Command, args []string) error {
	cwd, e := os.Getwd()
	if e != nil {
		return e
	}
	root, e := store.FindRoot(cwd)
	if e != nil {
		return e
	}
	st, e := store.NewStore(root)
	if e != nil {
		return e
	}
	addr, _ := cmd.Flags().GetString("listen")
	ln, e := net.Listen("tcp", addr)
	if e != nil {
		return e
	}
	defer ln.Close()
	app := &webui.Server{Store: st}
	own, _ := cmd.Flags().GetBool("supervise")
	if own {
		lock, e := tui.AcquireSupervisorLock(root)
		if e != nil {
			return e
		}
		defer lock.Close()
		app.Supervisor = supervisor.New(root)
		defer app.Supervisor.Shutdown()
		app.Supervisor.Start()
		app.Supervisor.EnqueuePendingActivityRoots()
	}
	server := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		case <-done:
		}
	}()
	fmt.Fprintf(cmd.OutOrStdout(), "Ostraka web: http://%s (supervisor owned: %t)\n", ln.Addr(), own)
	e = server.Serve(ln)
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
