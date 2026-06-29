package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"time"

	goadbruntime "github.com/asjdf/goadb/internal/goadbserver/runtime"
)

func main() {
	goadbruntime.PrependDLLSearchPath()

	cfg, err := goadbruntime.ParseConfig(os.Args[1:], goadbruntime.UserHomeDir())
	if err != nil {
		log.Fatal(err)
	}

	switch cfg.Mode {
	case goadbruntime.ModeStartServer:
		err = startDetachedServer(cfg)
	case goadbruntime.ModeNoDaemon:
		err = goadbruntime.Run(context.Background(), goadbruntime.Options{
			ListenAddr: cfg.ListenAddr,
			KeyPath:    cfg.KeyPath,
			InitialTCP: cfg.InitialTCP,
		})
	default:
		err = fmt.Errorf("unknown server mode %d", cfg.Mode)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func startDetachedServer(cfg goadbruntime.Config) error {
	conn, err := net.DialTimeout("tcp", cfg.ListenAddr, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"-L", "tcp:" + cfg.ListenAddr, "--key", cfg.KeyPath}
	for _, address := range cfg.InitialTCP {
		args = append(args, "--connect", address)
	}
	args = append(args, "server", "nodaemon")
	cmd := exec.Command(exe, args...)
	configureServerProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", cfg.ListenAddr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server did not start listening on %s", cfg.ListenAddr)
}
