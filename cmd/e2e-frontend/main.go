package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"bingotools/internal/app"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: e2e-frontend <bilibili|douyin> <id>")
		os.Exit(1)
	}
	platform, id := os.Args[1], os.Args[2]

	a := app.NewApp()
	a.StartupForTest(context.Background())
	res, err := a.ResolveLive(1, platform+":"+id)
	if err != nil {
		fmt.Println("ResolveLive error:", err)
		os.Exit(1)
	}
	fmt.Printf("resolved: kind=%s title=%q room=%s\n", res.Kind, res.Title, res.Room)

	mux := http.NewServeMux()
	mux.Handle("/live/", a.AssetsHandlerForTest())
	srv := &http.Server{Addr: "127.0.0.1:18099", Handler: mux}
	go func() { _ = srv.ListenAndServe() }()
	fmt.Println("proxy listening on http://127.0.0.1:18099/live/stream/1")

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	_ = srv.Close()
}
