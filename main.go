package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ktr0731/go-fuzzyfinder"
	"github.com/spf13/cobra"
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "gwt",
		Short: "Git Worktree Manager",
		Run:   runJump,
	}

	rootCmd.AddCommand(&cobra.Command{
		Use:   "add [branch]",
		Short: "Create a worktree from a branch",
		Args:  cobra.ExactArgs(1),
		Run:   runAdd,
	})

	removeCmd := &cobra.Command{
		Use:   "remove",
		Short: "Interactively remove a worktree",
		Run:   runRemove,
	}
	removeCmd.Flags().BoolP("force", "f", false, "Force removal")
	rootCmd.AddCommand(removeCmd)

	rootCmd.AddCommand(&cobra.Command{
		Use:     "main",
		Aliases: []string{"master"},
		Short:   "Jump to default branch worktree",
		Run:     runJumpDefault,
	})

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// --- Handlers ---

func runJump(cmd *cobra.Command, args []string) {
	if len(args) > 0 {
		path, err := findWorktreePathForBranch(args[0])
		if err != nil {
			fail(err)
		}
		printPath(path)
		return
	}

	worktrees, err := getWorktreesSorted() // <--- CHANGED
	if err != nil {
		fail(err)
	}

	idx, err := fuzzyfinder.Find(
		worktrees,
		func(i int) string { return worktrees[i] },
		// "Reverse" feel: Prompt at top (Search moves down) is the default in some libs,
		// but fuzzyfinder standard is bottom-up.
		// We use standard settings but the list is pre-sorted with current dir at top.
		fuzzyfinder.WithPreviewWindow(func(i int, w, h int) string {
			if i == -1 {
				return ""
			}
			path := strings.Fields(worktrees[i])[0]
			out, _ := exec.Command("git", "-C", path, "status", "--short").Output()
			return string(out)
		}),
	)
	if err != nil {
		os.Exit(1)
	}

	path := strings.Fields(worktrees[idx])[0]
	printPath(path)
}

func runAdd(cmd *cobra.Command, args []string) {
	branch := args[0]
	cwd, _ := os.Getwd()
	repoName := filepath.Base(cwd)
	sanitized := strings.ReplaceAll(branch, "/", "_")
	newPath := filepath.Join("..", fmt.Sprintf("%s_%s", repoName, sanitized))

	gitCmd := exec.Command("git", "worktree", "add", newPath, branch)
	gitCmd.Stderr = os.Stderr
	if err := gitCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Branch not found. Create '%s'? [y/N] ", branch)
		reader := bufio.NewReader(os.Stdin)
		res, _ := reader.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(res)) == "y" {
			gitCmd = exec.Command("git", "worktree", "add", "-b", branch, newPath)
			gitCmd.Stderr = os.Stderr
			if err := gitCmd.Run(); err != nil {
				fail(fmt.Errorf("failed to create worktree"))
			}
		} else {
			os.Exit(1)
		}
	}
	printPath(newPath)
}

func runRemove(cmd *cobra.Command, args []string) {
	worktrees, err := getWorktreesSorted()
	if err != nil {
		fail(err)
	}

	// Use FindMulti instead of Find
	idxs, err := fuzzyfinder.FindMulti(
		worktrees,
		func(i int) string { return worktrees[i] },
		fuzzyfinder.WithPreviewWindow(func(i int, w, h int) string {
			if i == -1 {
				return ""
			}
			path := strings.Fields(worktrees[i])[0]
			out, _ := exec.Command("git", "-C", path, "status", "--short").Output()
			return string(out)
		}),
	)
	if err != nil {
		os.Exit(1)
	}

	// Collect paths to remove
	var pathsToRemove []string
	for _, i := range idxs {
		pathsToRemove = append(pathsToRemove, strings.Fields(worktrees[i])[0])
	}

	// Confirm bulk action
	fmt.Fprintf(os.Stderr, "Remove %d worktree(s)?\n", len(pathsToRemove))
	for _, p := range pathsToRemove {
		fmt.Fprintf(os.Stderr, " - %s\n", p)
	}
	fmt.Fprintf(os.Stderr, "[y/N] ")

	reader := bufio.NewReader(os.Stdin)
	res, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(res)) != "y" {
		os.Exit(1)
	}

	// Execute removals
	force, _ := cmd.Flags().GetBool("force")
	baseArgs := []string{"worktree", "remove"}
	if force {
		baseArgs = append(baseArgs, "--force")
	}

	for _, path := range pathsToRemove {
		args := append(baseArgs, path)
		c := exec.Command("git", args...)
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove %s: %v\n", path, err)
		} else {
			fmt.Fprintf(os.Stderr, "Removed %s\n", path)
		}
	}

	// Finally, jump to repo root (so we don't stay in a deleted dir)
	out, _ := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	printPath(strings.TrimSpace(string(out)))
}

func runJumpDefault(cmd *cobra.Command, args []string) {
	for _, b := range []string{"main", "master"} {
		if p, err := findWorktreePathForBranch(b); err == nil {
			printPath(p)
			return
		}
	}
	out, _ := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	printPath(strings.TrimSpace(string(out)))
}

// --- Helpers ---

func getWorktreesSorted() ([]string, error) {
	out, err := exec.Command("git", "worktree", "list").Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	// Get Current Working Directory
	cwd, _ := os.Getwd()
	// Resolve symlinks just in case
	cwd, _ = filepath.EvalSymlinks(cwd)

	// Sort: Current dir first, then alphabetical
	sort.SliceStable(lines, func(i, j int) bool {
		pathI := strings.Fields(lines[i])[0]
		pathJ := strings.Fields(lines[j])[0]

		// Check if pathI matches CWD
		if pathI == cwd {
			return true
		} // I comes first
		if pathJ == cwd {
			return false
		} // J comes first (so I is later)

		return pathI < pathJ // Alphabetical default
	})

	return lines, nil
}

func findWorktreePathForBranch(branch string) (string, error) {
	wts, _ := getWorktreesSorted()
	for _, l := range wts {
		if strings.Contains(l, "["+branch+"]") {
			return strings.Fields(l)[0], nil
		}
	}
	return "", fmt.Errorf("branch not found")
}

func printPath(p string) { fmt.Println(p) }
func fail(err error)     { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
