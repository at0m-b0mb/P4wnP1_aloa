package cli_client

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mame82/P4wnP1_aloa/duckyscript"
	"github.com/spf13/cobra"
)

// --- Top-level "ducky" command ----------------------------------------------

var duckyCmd = &cobra.Command{
	Use:   "ducky",
	Short: "Work with DuckyScript payloads",
	Long: `Work with DuckyScript payloads.

Most published payloads for USB HID injection are written in DuckyScript.
P4wnP1 runs HIDScript, which is real JavaScript and strictly more capable, but
almost nothing is written in it. 'ducky convert' translates a DuckyScript
payload into HIDScript you can read, edit and run here.

Conversion is offline and inert: it reads text and writes text. It never
touches a keyboard, a device file or the network, and it does not need a
connection to the service.`,
}

// --- "ducky convert" ---------------------------------------------------------

var (
	duckyOut      string
	duckyLayout   string
	duckySpeed    int
	duckyJitter   int
	duckyStrict   bool
	duckyListKeys bool
)

var duckyConvertCmd = &cobra.Command{
	Use:   "convert [payload.txt]",
	Short: "Convert a DuckyScript payload to HIDScript",
	Long: `Convert a DuckyScript payload to HIDScript.

Reads from the named file, or from stdin if no file is given. Writes to stdout
unless -o is used.

DuckyScript 1.0 is covered in full. Later Hak5 dialects (2.0/3.0 variables,
conditionals, functions, EXFIL) and Bash Bunny directives have no direct
HIDScript equivalent; those lines are reported as warnings AND left in the
output as "// UNCONVERTED:" comments, so a payload never silently loses part of
its logic. Use --strict to make any such line a hard failure instead.

DuckyScript carries no layout information -- it assumes the target's keyboard
matches whatever the payload's author used. Set --layout to one of the keymaps
in /usr/local/P4wnP1/keymaps (US, DE_ASCII, gb, fr, es, it, ...) to pin it.

Examples:
  P4wnP1_cli ducky convert payload.txt
  P4wnP1_cli ducky convert payload.txt -o payload.js --layout gb
  cat payload.txt | P4wnP1_cli ducky convert --speed 80 --jitter 20`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if duckyListKeys {
			fmt.Println(strings.Join(duckyscript.SupportedKeys(), " "))
			return nil
		}

		var src []byte
		var err error
		if len(args) == 1 && args[0] != "-" {
			src, err = os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("reading %s: %w", args[0], err)
			}
		} else {
			src, err = io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("reading stdin: %w", err)
			}
		}

		out, warnings := duckyscript.Convert(string(src), duckyscript.Options{
			Layout:             duckyLayout,
			TypingSpeedMillis:  duckySpeed,
			TypingJitterMillis: duckyJitter,
		})

		// Warnings go to stderr so the converted script can still be piped.
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		if duckyStrict && len(warnings) > 0 {
			return fmt.Errorf("%d line(s) could not be converted and --strict is set", len(warnings))
		}

		if duckyOut != "" {
			if err := os.WriteFile(duckyOut, []byte(out), 0644); err != nil {
				return fmt.Errorf("writing %s: %w", duckyOut, err)
			}
			fmt.Fprintf(os.Stderr, "wrote %s", duckyOut)
			if len(warnings) > 0 {
				fmt.Fprintf(os.Stderr, " (%d warning(s) above -- review before running)", len(warnings))
			}
			fmt.Fprintln(os.Stderr)
			return nil
		}
		fmt.Print(out)
		return nil
	},
}

func init() {
	duckyConvertCmd.Flags().StringVarP(&duckyOut, "out", "o", "",
		"write the HIDScript here instead of stdout")
	duckyConvertCmd.Flags().StringVar(&duckyLayout, "layout", "",
		"keyboard layout to pin (US, DE_ASCII, gb, fr, ...); omit to use the device default")
	duckyConvertCmd.Flags().IntVar(&duckySpeed, "speed", 0,
		"milliseconds between keystrokes (0 = leave the device default)")
	duckyConvertCmd.Flags().IntVar(&duckyJitter, "jitter", 0,
		"random variation added to --speed, in milliseconds")
	duckyConvertCmd.Flags().BoolVar(&duckyStrict, "strict", false,
		"fail if any line cannot be converted, instead of emitting it as a comment")
	duckyConvertCmd.Flags().BoolVar(&duckyListKeys, "list-keys", false,
		"print every DuckyScript key name the converter understands, and exit")

	duckyCmd.AddCommand(duckyConvertCmd)
	rootCmd.AddCommand(duckyCmd)
}
