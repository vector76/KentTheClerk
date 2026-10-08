package mimetypes

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// readRegistry is MimeTypes.read_windows_registry(): every HKEY_CLASSES_ROOT subkey whose name starts
// with "." and has a non-empty REG_SZ "Content Type" value maps that extension to that type.
func readRegistry(add func(typ, ext string)) {
	hkcr, err := registry.OpenKey(registry.CLASSES_ROOT, "", registry.READ)
	if err != nil {
		return
	}
	defer hkcr.Close()
	names, err := hkcr.ReadSubKeyNames(-1)
	if err != nil && len(names) == 0 {
		return
	}
	for _, ext := range names {
		if ext != "" && !strings.HasPrefix(ext, ".") {
			continue
		}
		k, err := registry.OpenKey(hkcr, ext, registry.READ)
		if err != nil {
			continue
		}
		typ, valtype, err := k.GetStringValue("Content Type")
		k.Close()
		if err != nil || valtype != registry.SZ {
			continue
		}
		if i := strings.IndexByte(typ, 0); i >= 0 {
			typ = typ[:i]
		}
		add(typ, ext)
	}
}
