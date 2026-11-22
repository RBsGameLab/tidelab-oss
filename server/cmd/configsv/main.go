// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// tidelab configsv: the HTTP config and CDN server (specs/config.md).
package main

import (
	"os"

	"tidelab/internal/cfgserver"
	"tidelab/internal/common"
	"tidelab/internal/httpserv"
)

func main() {
	common.Info("init", common.Banner("configsv"))
	os.Exit(httpserv.Main(os.Args[1:], "config server", map[string]string{
		"listen_address": "127.0.0.1:5500",
		"state_dir":      "state",
	}, func(o *common.Options) (httpserv.Handler, error) {
		return cfgserver.NewService(o.Get("state_dir")).Handle, nil
	}))
}
