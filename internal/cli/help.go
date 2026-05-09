package cli

import (
	"fmt"
	"io"
)

// runHelp dispatches `lucinate help [<command>]` by writing the
// relevant usage block to out. An unknown command name surfaces as
// an error so Run can render it alongside the top-level usage block
// on stderr.
func runHelp(args []string, out io.Writer) error {
	if len(args) == 0 {
		printTopUsage(out)
		return nil
	}
	switch args[0] {
	case "send":
		printSendUsage(out)
	case "chat":
		printChatUsage(out)
	case "help":
		printTopUsage(out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

// printTopUsage writes the top-level lucinate usage block.
func printTopUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: lucinate [--version|-v] [<command> [args...]]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "lucinate is a terminal-native AI chat client. Without a command, it")
	fmt.Fprintln(out, "launches the interactive TUI; with one, it runs that command's flow.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  send    Dispatch a single message and print the reply (one-shot, no TUI)")
	fmt.Fprintln(out, "  chat    Launch the TUI pre-navigated to a connection / agent / session")
	fmt.Fprintln(out, "  help    Show help for lucinate or a specific command")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Flags:")
	fmt.Fprintln(out, "  -h, --help       Show this help and exit")
	fmt.Fprintln(out, "  -v, --version    Print version and exit")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Run 'lucinate help <command>' for command-specific usage.")
}
