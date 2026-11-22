// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// tidelab loginsv: the HTTP login server (specs/login.md).
package main

import (
	"os"

	"tidelab/internal/common"
	"tidelab/internal/httpserv"
	"tidelab/internal/login"
)

func main() {
	common.Info("init", common.Banner("loginsv"))
	os.Exit(httpserv.Main(os.Args[1:], "login server", map[string]string{
		"listen_address":  "127.0.0.1:10001",
		"state_dir":       "state",
		"gateway_address": "127.0.0.1:13100",
	}, func(o *common.Options) (httpserv.Handler, error) {
		gw, err := o.Endpoint("gateway_address")
		if err != nil {
			return nil, err
		}
		return login.NewService(login.NewAccounts(o.Get("state_dir")), gw).Handle, nil
	}))
}
