package main

import "sync/atomic"

// UI language of the background process (tray, notifications, popups, errors).
var langRU atomic.Bool

// T picks the Russian or English variant of a message.
func T(ru, en string) string {
	if langRU.Load() {
		return ru
	}
	return en
}

func setLang(l string) { langRU.Store(l == "ru") }

func currentLang() string {
	if langRU.Load() {
		return "ru"
	}
	return "en"
}
