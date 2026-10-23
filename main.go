// Pracre: value-per-acre maps for Strong Towns advocates.
// Copyright (C) 2026 Strong Towns contributors. Licensed under the GNU GPL v3 or later.
package main

import (
	"context"
	"embed"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Version is set at build time with -ldflags "-X main.Version=..."
var Version = "dev"

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:            "Pracre",
		Width:            1440,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        680,
		BackgroundColour: &options.RGBA{R: 236, G: 238, B: 242, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets, Handler: app},
		Menu:             app.menu(),
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		Bind:             []any{app},
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true, DisableWebViewDrop: true},
		Windows:          &windows.Options{Theme: windows.Light},
		Mac: &mac.Options{
			Appearance: mac.NSAppearanceNameAqua,
			About:      &mac.AboutInfo{Title: "Pracre " + Version, Message: "Value per acre maps for Strong Towns advocates.\nGNU GPL v3", Icon: icon},
			OnFileOpen: app.openFromSystem,
		},
		Linux: &linux.Options{Icon: icon, ProgramName: "pracre", WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}

// menu builds the native menu bar; each item fires a "menu" event the front end handles like a toolbar click.
func (a *App) menu() *menu.Menu {
	m := menu.NewMenu()
	if runtime.GOOS == "darwin" {
		m.Append(menu.AppMenu())
	}
	fire := func(action string) func(*menu.CallbackData) {
		return func(*menu.CallbackData) { wr.EventsEmit(a.ctx, "menu", action) }
	}
	file := m.AddSubmenu("File")
	file.AddText("New Project", keys.CmdOrCtrl("n"), fire("new"))
	file.AddText("Open Project…", keys.CmdOrCtrl("o"), fire("open"))
	file.AddText("Save Project", keys.CmdOrCtrl("s"), fire("save"))
	file.AddText("Save Project As…", keys.Combo("s", keys.CmdOrCtrlKey, keys.ShiftKey), fire("saveas"))
	file.AddSeparator()
	file.AddText("Import Data…", keys.CmdOrCtrl("i"), fire("import"))
	file.AddText("Load Sample Data (Township of Langley)", nil, fire("sample"))
	if runtime.GOOS != "darwin" {
		file.AddSeparator()
		file.AddText("Exit", keys.OptionOrAlt("f4"), func(*menu.CallbackData) { wr.Quit(a.ctx) })
	}
	if runtime.GOOS == "darwin" {
		m.Append(menu.EditMenu())
	}
	data := m.AddSubmenu("Data")
	data.AddText("Tax Rates…", keys.CmdOrCtrl("t"), fire("rates"))
	data.AddText("Data Checklist", nil, fire("checklist"))
	view := m.AddSubmenu("View")
	view.AddText("2D Map", keys.CmdOrCtrl("2"), fire("view2d"))
	view.AddText("3D Map", keys.CmdOrCtrl("3"), fire("view3d"))
	view.AddSeparator()
	view.AddText("Parcels", keys.CmdOrCtrl("p"), fire("parcels"))
	view.AddText("Hex Tiles", keys.CmdOrCtrl("h"), fire("hex"))
	view.AddText("Square Tiles", keys.CmdOrCtrl("g"), fire("square"))
	view.AddSeparator()
	view.AddText("Zoom to Data", keys.CmdOrCtrl("0"), fire("fit"))
	exp := m.AddSubmenu("Export")
	exp.AddText("Image (PNG)…", keys.CmdOrCtrl("e"), fire("export-image"))
	exp.AddSeparator()
	exp.AddText("3D Web Map (HTML)…", nil, fire("export-web3d"))
	exp.AddText("3D Model (glTF .glb)…", nil, fire("export-glb"))
	exp.AddSeparator()
	exp.AddText("2D Web Map (HTML)…", nil, fire("export-web2d"))
	exp.AddText("GeoJSON…", nil, fire("export-geojson"))
	exp.AddText("CSV Table…", nil, fire("export-csv"))
	help := m.AddSubmenu("Help")
	help.AddText("Getting Started", keys.Key("f1"), fire("help"))
	help.AddText("STCD Website", nil, func(*menu.CallbackData) { wr.BrowserOpenURL(a.ctx, "https://strongtowns.dev") })
	help.AddText("Pracre on GitHub", nil, func(*menu.CallbackData) { wr.BrowserOpenURL(a.ctx, "https://github.com/strongtowns/Pracre") })
	help.AddSeparator()
	help.AddText("About Pracre", nil, fire("about"))
	return m
}

func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	dirty := a.dirty
	a.mu.Unlock()
	if !dirty {
		return false
	}
	res, err := wr.MessageDialog(ctx, wr.MessageDialogOptions{
		Type: wr.QuestionDialog, Title: "Pracre", Message: "Close without saving the project?",
		Buttons: []string{"Close", "Cancel"}, DefaultButton: "Cancel", CancelButton: "Cancel",
	})
	return err == nil && res != "Close" && res != "Yes"
}
