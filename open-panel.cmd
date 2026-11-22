@echo off
rem Opens tidelab's content panel (R-160) in the browser: the page gamesv serves on this
rem machine only. The server must be running (start-server.cmd); its log prints the address.
start "" "http://127.0.0.1:13180/"
