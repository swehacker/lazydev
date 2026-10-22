// Command lazydev is a terminal UI for the local Docker, Compose and k3d development stack.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/swehacker/lazydev/internal/app"
	"github.com/swehacker/lazydev/internal/config"
)

var version = "dev"

func main() {
	var (
		configPath  = flag.String("config", "", "path to lazydev.yaml (default: ./lazydev.yaml, git root, ~/.config/lazydev/config.yaml)")
		kubeContext = flag.String("context", "", "kubeconfig context to use (overrides config)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("lazydev", version)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazydev:", err)
		os.Exit(2)
	}
	if *kubeContext != "" {
		cfg.Cluster.KubeContext = *kubeContext
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = app.Run(ctx, cfg, version)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lazydev:", err)
		os.Exit(1)
	}
}
