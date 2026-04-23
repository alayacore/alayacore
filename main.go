package main

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/adaptors/plainio"
	"github.com/alayacore/alayacore/internal/adaptors/terminal"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/config"
)

func main() {
	cfg := config.Parse()

	if cfg.ShowVersion {
		fmt.Printf("alayacore version %s\n", config.Version)
		os.Exit(0)
	}

	appCfg, err := app.Setup(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if cfg.PlainIO {
		adaptor := plainio.NewAdaptor(appCfg)
		os.Exit(adaptor.Start())
	}

	adaptor := terminal.NewAdaptorWithThemes(appCfg, cfg.ThemesFolder)
	adaptor.Start()
}
