package main

import (
	"github.com/Wei-Shaw/sub2api/internal/openaitransportplugin"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
)

var version = "0.0.0-dev"

func main() {
	pluginv1.Serve(openaitransportplugin.New(version))
}
