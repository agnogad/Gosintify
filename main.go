package main

import (
	"log"

	"github.com/egoist/mygo"
)

func main() {
	ensurePath()
	api := &Api{}
	mygo.Bind(api)

	mygo.App.WhenReady(func() {
		api.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "Gosintify",
			Width:           1100,
			Height:          780,
			MinWidth:        760,
			MinHeight:       560,
			BackgroundColor: "light-dark(#f7f7f8, #111113)",
			StateKey:        "main",
			URL:             "/",
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
