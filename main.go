package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"bingotools/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	a := app.NewApp()

	err := wails.Run(&options.App{
		Title:     "BingoTools",
		Width:     1920,
		Height:    1080,
		MinWidth:  1280,
		MinHeight: 720,
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: a.AssetsHandlerForTest(),
		},
		OnStartup: a.OnStartup,
		Bind: []interface{}{
			a,
		},
	})
	if err != nil {
		log.Fatalf("wails run: %v", err)
	}
}
