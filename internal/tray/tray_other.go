//go:build !windows

package tray

import "errors"

func Run(string) error {
	return errors.New("the notification area icon is only available on Windows")
}
