package main

import (
	"context"
	"os"

	"github.com/mheers/sipgate-golang/internal/cli"
)

func main() {
	app := cli.NewApp()
	app.Out = os.Stdout
	app.Err = os.Stderr
	os.Exit(app.Execute(context.Background()))
}
