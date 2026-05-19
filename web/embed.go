package web

import "embed"

// Static holds mobile-first UI assets (see web/static/).
//
//go:embed static/*
var Static embed.FS
