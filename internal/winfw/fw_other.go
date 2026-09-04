//go:build !windows

package winfw

func Allow(string) error { return nil }
func Remove() error      { return nil }
