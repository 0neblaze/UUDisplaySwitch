package main

import "testing"

func TestStoragePathRejectsPackageCache(t *testing.T) {
	real := `C:\Users\Example\AppData\Local\UUDisplaySwitch\monitor.json`
	cache := `C:\Users\Example\AppData\Local\Packages\Example.TestPackage_123\LocalCache\Local\UUDisplaySwitch\monitor.json`
	if sameStoragePath(real, cache) {
		t.Fatal("package-private heartbeat accepted as desktop heartbeat")
	}
	if !sameStoragePath(real, `c:\users\Example\AppData\Local\UUDisplaySwitch\monitor.json`) {
		t.Fatal("same Windows path rejected")
	}
}
