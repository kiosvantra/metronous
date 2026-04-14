//go:build !linux

package cli

func postUpdateInstallMigration(binaryPath string) error {
	return nil
}
