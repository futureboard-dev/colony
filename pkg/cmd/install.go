package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Symlink the colony binary into ~/.local/bin so it is available system-wide",
	Long: `Install creates a symlink from ~/.local/bin/colony to the running binary.

After installation you can run 'colony' from any directory without a './' prefix.
The symlink points at the current binary, so rebuilding (go build -o colony .) is
enough to update it — no need to re-run install.

Prerequisites:
  - ~/.local/bin must exist on your PATH.
    Add the following line to ~/.zshrc or ~/.bashrc if it is not already there:
      export PATH="$HOME/.local/bin:$PATH"

To uninstall, run: colony uninstall`,
	RunE: runInstall,
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the colony symlink from ~/.local/bin",
	Long:  `Uninstall removes the symlink created by 'colony install'. The binary itself is not deleted.`,
	RunE:  runUninstall,
}

func localBinPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin", "colony"), nil
}

func runInstall(cmd *cobra.Command, args []string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot resolve current executable: %w", err)
	}
	// Resolve any existing symlink so we point at the real binary.
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("cannot resolve symlinks on current executable: %w", err)
	}

	dest, err := localBinPath()
	if err != nil {
		return err
	}

	// Ensure ~/.local/bin exists.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(dest), err)
	}

	// Remove a stale symlink or file at the target location.
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Remove(dest); err != nil {
			return fmt.Errorf("cannot remove existing %s: %w", dest, err)
		}
	}

	if err := os.Symlink(self, dest); err != nil {
		return fmt.Errorf("cannot create symlink: %w", err)
	}

	fmt.Printf("✓ Installed: %s → %s\n", dest, self)
	fmt.Printf("  Make sure ~/.local/bin is on your PATH:\n")
	fmt.Printf("    export PATH=\"$HOME/.local/bin:$PATH\"\n")
	fmt.Printf("  Then run: colony version\n")
	return nil
}

func runUninstall(cmd *cobra.Command, args []string) error {
	dest, err := localBinPath()
	if err != nil {
		return err
	}

	if _, err := os.Lstat(dest); os.IsNotExist(err) {
		fmt.Printf("nothing to remove — %s does not exist\n", dest)
		return nil
	}

	if err := os.Remove(dest); err != nil {
		return fmt.Errorf("cannot remove %s: %w", dest, err)
	}

	fmt.Printf("✓ Removed %s\n", dest)
	return nil
}
