//go:build android || ios || wasm

package main

func sanitizeFynePreferencesBeforeLoad(uniqueID string) {}

func fyneUpdateCheckStatePath(uniqueID string) string { return "" }
