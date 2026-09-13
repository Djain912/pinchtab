package main

import (
	browseractions "github.com/pinchtab/pinchtab/internal/cli/actions"
	"github.com/spf13/cobra"
)

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "JavaScript heap usage, heap snapshots and their summaries",
	Long: `Read the tab's JavaScript heap usage and DOM counters.

Pass --gc to run a garbage collection first so two reads compare live memory only.
Subcommands: snapshot writes a V8 heap snapshot to a server-side file, and summary
lists its top constructors and duplicate strings. Both need security.allowMemory,
because a heap snapshot holds every string on the page, tokens included.`,
	Run: func(cmd *cobra.Command, args []string) {
		runCLI(func(rt cliRuntime) {
			browseractions.Memory(rt.client, rt.base, rt.token, cmd)
		})
	},
}

var memorySnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Take a V8 heap snapshot to a server-side file",
	Long: `Take a V8 heap snapshot of the tab. The server streams it to its own
heapsnapshots directory and prints the id, path, size and node count; pass the id
to 'pinchtab memory summary'. --out copies the file to a local path when the server
shares this machine's filesystem. The file loads in Chrome DevTools (Memory panel).`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		runCLI(func(rt cliRuntime) {
			browseractions.MemorySnapshot(rt.client, rt.base, rt.token, cmd)
		})
	},
}

var memorySummaryCmd = &cobra.Command{
	Use:   "summary <id>",
	Short: "Summarize a saved heap snapshot",
	Long:  "Summarize a saved heap snapshot: top constructors by self size and by count, total node and edge counts, and the largest duplicate strings.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runCLI(func(rt cliRuntime) {
			browseractions.MemorySummary(rt.client, rt.base, rt.token, cmd, args[0])
		})
	},
}

func configureMemoryFlags() {
	memoryCmd.Flags().Bool("gc", false, "Collect garbage before reading")
	memoryCmd.Flags().String("tab", "", "Target tab ID")
	memoryCmd.Flags().Bool("json", false, "Output raw JSON")
	memorySnapshotCmd.Flags().String("tab", "", "Target tab ID")
	memorySnapshotCmd.Flags().String("out", "", "Copy the snapshot to this local path")
	memorySnapshotCmd.Flags().Bool("json", false, "Output raw JSON")
	memorySummaryCmd.Flags().Int("top", 20, "Rows per table (max 200)")
	memorySummaryCmd.Flags().Bool("json", false, "Output raw JSON")
}
