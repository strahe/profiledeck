//go:build !darwin

package main

func systemPreferredTrayLanguage() string {
	return environmentPreferredTrayLanguage()
}
