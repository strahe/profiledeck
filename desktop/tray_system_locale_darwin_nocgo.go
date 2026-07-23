//go:build darwin && !cgo

package main

func systemPreferredTrayLanguage() string {
	return environmentPreferredTrayLanguage()
}
