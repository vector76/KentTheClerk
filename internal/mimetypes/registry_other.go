//go:build !windows

package mimetypes

func readRegistry(add func(typ, ext string)) {}
