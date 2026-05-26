package cli_client

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// --- Top-level "auth" command --------------------------------------------------

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate against the P4wnP1 service (login, logout, whoami, changepw)",
	Long: `Authenticate against the P4wnP1 service.

After a successful 'auth login', the CLI caches a bearer token at
~/.p4wnp1/token (mode 0600) and automatically attaches it to every
subsequent RPC call. Tokens expire after 24 hours (sliding window
on each use).

Find the admin password on a fresh install in /root/INITIAL_CREDENTIALS.txt
on the Pi.`,
}

// --- "auth login" --------------------------------------------------------------

var (
	loginUsername string
	loginPassword string
)

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in and cache a bearer token at ~/.p4wnp1/token",
	Long: `Log in to the P4wnP1 service and cache the resulting bearer token
at ~/.p4wnp1/token (mode 0600). Subsequent CLI calls authenticate
automatically.

If --password is not supplied, the command reads one line from stdin
(use 'read -s' in a shell pipeline to avoid leaking the password
into shell history).

Examples:
    P4wnP1_cli auth login --user admin --password 'verysecret'
    read -s pw && echo "$pw" | P4wnP1_cli auth login --user admin
    P4wnP1_cli --host 172.24.0.1 auth login --user admin --password ...`,
	Run: runAuthLogin,
}

func runAuthLogin(cmd *cobra.Command, args []string) {
	password := loginPassword
	if password == "" {
		// Read a single line from stdin; trim the trailing newline.
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "could not read password from stdin: %v\n", err)
			os.Exit(1)
		}
		password = strings.TrimRight(line, "\r\n")
		if password == "" {
			fmt.Fprintln(os.Stderr, "error: --password not supplied and stdin is empty")
			fmt.Fprintln(os.Stderr, "       pass --password '<pw>' or pipe the password into stdin")
			os.Exit(2)
		}
	}

	resp, err := apiLogin(StrRemoteHost, loginUsername, password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "login failed: %v\n", err)
		os.Exit(1)
	}
	if err := SaveToken(StrRemoteHost, resp.Username, resp.Token, resp.ExpiresAt); err != nil {
		fmt.Fprintf(os.Stderr, "warning: login succeeded but couldn't save token: %v\n", err)
		fmt.Fprintf(os.Stderr, "         token (use Authorization: Bearer ...): %s\n", resp.Token)
		os.Exit(1)
	}
	expiresIn := time.Until(time.Unix(resp.ExpiresAt, 0)).Round(time.Second)
	fmt.Printf("logged in as %s on %s (token valid for ~%s)\n",
		resp.Username, StrRemoteHost, expiresIn)
}

// --- "auth logout" -------------------------------------------------------------

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke the cached token (server-side) and delete it locally",
	Run: func(cmd *cobra.Command, args []string) {
		host, _, token, _, err := LoadToken()
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not read cached token: %v\n", err)
			os.Exit(1)
		}
		if token == "" {
			fmt.Println("no cached token; nothing to do")
			return
		}
		// Server-side revoke is best-effort. Even if it fails (e.g. service
		// unreachable, token already expired), we still wipe the local copy.
		if err := apiLogout(host, token); err != nil {
			fmt.Fprintf(os.Stderr, "warning: server-side revoke failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "         deleting local token anyway")
		}
		if err := ClearToken(); err != nil {
			fmt.Fprintf(os.Stderr, "could not delete local token: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("logged out")
	},
}

// --- "auth whoami" -------------------------------------------------------------

var authWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the currently-logged-in user (from local cache + server check)",
	Run: func(cmd *cobra.Command, args []string) {
		host, cachedUser, token, expiresAt, err := LoadToken()
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not read token cache: %v\n", err)
			os.Exit(1)
		}
		if token == "" {
			fmt.Println("not logged in -- run 'P4wnP1_cli auth login'")
			os.Exit(1)
		}
		// Authoritative check against the server.
		who, err := apiWhoami(host, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "token invalid (cached as %q): %v\n", cachedUser, err)
			fmt.Fprintln(os.Stderr, "run 'P4wnP1_cli auth login' to refresh")
			os.Exit(1)
		}
		expiresIn := time.Until(time.Unix(who.ExpiresAt, 0)).Round(time.Second)
		fmt.Printf("user:       %s\n", who.Username)
		fmt.Printf("host:       %s\n", host)
		fmt.Printf("expires_in: %s\n", expiresIn)
		fmt.Printf("expires_at: %s\n", time.Unix(who.ExpiresAt, 0).Format(time.RFC3339))
		_ = expiresAt // suppress unused warning
	},
}

// --- "auth changepw" -----------------------------------------------------------

var (
	changepwUsername string
	changepwOld      string
	changepwNew      string
)

var authChangepwCmd = &cobra.Command{
	Use:   "changepw",
	Short: "Change a user's password (all sessions are revoked server-side)",
	Long: `Change an account's password. The server revokes ALL existing sessions
for the user when the change succeeds, so every connected client (including
this CLI) must log in again afterwards.

If --old / --new are not supplied, the command reads them from stdin as
two newline-separated lines:

    printf '%s\n%s\n' "$OLD_PW" "$NEW_PW" | P4wnP1_cli auth changepw --user admin`,
	Run: runAuthChangepw,
}

func runAuthChangepw(cmd *cobra.Command, args []string) {
	oldPw, newPw := changepwOld, changepwNew

	if oldPw == "" || newPw == "" {
		r := bufio.NewReader(os.Stdin)
		if oldPw == "" {
			l, err := r.ReadString('\n')
			if err != nil && err != io.EOF {
				fmt.Fprintf(os.Stderr, "read old password: %v\n", err)
				os.Exit(1)
			}
			oldPw = strings.TrimRight(l, "\r\n")
		}
		if newPw == "" {
			l, err := r.ReadString('\n')
			if err != nil && err != io.EOF {
				fmt.Fprintf(os.Stderr, "read new password: %v\n", err)
				os.Exit(1)
			}
			newPw = strings.TrimRight(l, "\r\n")
		}
	}
	if oldPw == "" || newPw == "" {
		fmt.Fprintln(os.Stderr, "error: both old and new password are required")
		os.Exit(2)
	}
	if len(newPw) < 12 {
		fmt.Fprintln(os.Stderr, "error: new password must be at least 12 characters")
		os.Exit(2)
	}
	if err := apiChangePassword(StrRemoteHost, changepwUsername, oldPw, newPw); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	// Server revoked all sessions including ours; wipe local cache so the
	// user sees a clear "logged out" state and is forced to re-login.
	_ = ClearToken()
	fmt.Println("password changed; all existing sessions revoked. Run 'auth login' again.")
}

// --- wiring --------------------------------------------------------------------

func init() {
	authLoginCmd.Flags().StringVar(&loginUsername, "user", "admin", "username to log in as")
	authLoginCmd.Flags().StringVar(&loginPassword, "password", "", "password (omit to read from stdin)")

	authChangepwCmd.Flags().StringVar(&changepwUsername, "user", "admin", "username whose password to change")
	authChangepwCmd.Flags().StringVar(&changepwOld, "old", "", "current password (omit to read from stdin)")
	authChangepwCmd.Flags().StringVar(&changepwNew, "new", "", "new password, 12+ chars (omit to read from stdin)")

	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authWhoamiCmd)
	authCmd.AddCommand(authChangepwCmd)
	rootCmd.AddCommand(authCmd)
}
