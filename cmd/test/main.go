package main

import (
	"fmt"
	"log"
	"time"

	. "github.com/kungfusheep/glyph"
)

func main() {
	app := NewApp()

	pane := 0
	status := "ready"
	frame := 0

	folders := []string{"Inbox", "Sent", "Drafts", "Trash"}
	folderSel := 0

	threads := []string{"deploy pipeline [4]", "weekly sync [2]", "api review [7]"}
	threadSel := 0

	preview := []string{"(select a thread)"}

	folderBorder := White
	threadBorder := BrightBlack
	previewBorder := BrightBlack

	updateBorders := func() {
		folderBorder = BrightBlack
		threadBorder = BrightBlack
		previewBorder = BrightBlack
		switch pane {
		case 0:
			folderBorder = White
		case 1:
			threadBorder = White
		case 2:
			previewBorder = White
		}
	}

	selectedStyle := Style{Attr: AttrInverse}

	// test 1: just the view with spinner - does input work?
	app.View("main",
		VBox(
			HBox.Gap(1)(
				Text("mail").Bold(),
				Space(),
				Text(&status).Dim(),
				Space(),
				Spinner(&frame).Frames(SpinnerDots),
			),
			HBox(
				VBox.Grow(1).Border(BorderRounded).BorderFG(&folderBorder).Title("Folders")(
					List(&folders).Selection(&folderSel).SelectedStyle(selectedStyle).Marker("  "),
				),
				VBox.Grow(2).Border(BorderRounded).BorderFG(&threadBorder).Title("Threads")(
					List(&threads).Selection(&threadSel).SelectedStyle(selectedStyle).Marker("  "),
				),
				VBox.Grow(3).Border(BorderRounded).BorderFG(&previewBorder).Title("Preview")(
					ForEach(&preview, func(line *string) any {
						return Text(line)
					}),
				),
			),
			Text("q quit  h/l pane  j/k nav  enter select").Dim(),
		),
	).NoCounts().
		Handle("q", app.Stop).
		Handle("j", func() {
			switch pane {
			case 0:
				if folderSel < len(folders)-1 {
					folderSel++
				}
			case 1:
				if threadSel < len(threads)-1 {
					threadSel++
				}
			}
		}).
		Handle("k", func() {
			switch pane {
			case 0:
				if folderSel > 0 {
					folderSel--
				}
			case 1:
				if threadSel > 0 {
					threadSel--
				}
			}
		}).
		Handle("l", func() {
			if pane < 2 {
				pane++
				updateBorders()
			}
		}).
		Handle("h", func() {
			if pane > 0 {
				pane--
				updateBorders()
			}
		}).
		Handle("<Enter>", func() {
			status = fmt.Sprintf("selected pane %d", pane)
		})

	// add a second view with form — does this break input on main?
	var to, subject string
	app.View("compose",
		VBox(
			Text("compose view").Bold(),
			Form.LabelBold()(
				Field("To", Input(&to).Placeholder("recipient")),
				Field("Subject", Input(&subject).Placeholder("subject")),
			),
			Text("ctrl-q to go back").Dim(),
		),
	).NoCounts().Handle("<C-q>", func() {
		app.Go("main")
	})

	go func() {
		for range time.Tick(80 * time.Millisecond) {
			frame++
			app.RequestRender()
		}
	}()

	if err := app.RunFrom("main"); err != nil {
		log.Fatal(err)
	}
}
