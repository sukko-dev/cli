package commands

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
)

func init() {
	licenseCmd.AddCommand(licenseLoginCmd, licenseLogoutCmd)
}

// licenseLogoutCmd pairs with login: it forgets the stored key. It reuses
// runLicenseRemove (same behavior as 'sukko license remove') — there is no
// server-side session to revoke; the key is the credential.
var licenseLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out — remove the stored license key from the active context",
	Long:  "Remove the Sukko license key from the active context. An alias for 'sukko license remove'.",
	RunE:  runLicenseRemove,
}

// defaultLicenseAPIURL is the hosted Sukko license service. SUKKO_LICENSE_API_URL
// overrides it for staging/local testing.
const defaultLicenseAPIURL = "https://api.sukko.dev"

// Poll tuning — vars (not consts) so tests can shrink the waits. minPollInterval
// floors the server-advised interval so a 0 from a misbehaving server cannot
// tight-loop; slowDownIncrement is added on each SLOW_DOWN; a run aborts only after
// maxConsecutiveTransientFailures back-to-back transport/5xx/429 errors (so a
// single blip during the human-in-the-loop wait does not abort login).
var (
	minPollInterval                 = time.Second
	slowDownIncrement               = 5 * time.Second
	maxConsecutiveTransientFailures = 5
)

func licenseAPIURL() string { return envOrDefault("SUKKO_LICENSE_API_URL", defaultLicenseAPIURL) }

var licenseLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to your Sukko account to fetch and store your license key",
	Long: `Fetch your Sukko license key by logging in to your Sukko account in a browser
(no key to copy-paste). Starts an OAuth 2.0 device flow against the Sukko license
service: the CLI shows a short code and a URL, you approve in the browser, and the
key is stored (encrypted) in the active context — the same slot 'sukko license set'
uses, so 'sukko up' and 'sukko license show' pick it up.

The inverse is 'sukko license remove', which forgets the stored key.`,
	RunE: runLicenseLogin,
}

func runLicenseLogin(cmd *cobra.Command, _ []string) error {
	store, ctx, err := requireActiveContext()
	if err != nil {
		return err
	}

	// Ctrl-C / SIGTERM cancels the wait cleanly (returns a non-nil error → exit 1,
	// as nothing was stored).
	signalCtx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	c := client.NewLicenseDeviceClient(licenseAPIURL())
	auth, err := c.Authorize(signalCtx)
	if err != nil {
		return fmt.Errorf("start device login: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "To connect this device, open:\n\n    %s\n\nand enter the code:  %s\n\n", auth.VerificationURI, auth.UserCode)
	if auth.VerificationURIComplete != "" {
		fmt.Fprintf(out, "Or open this link, which fills the code in for you:\n    %s\n\n", auth.VerificationURIComplete)
	}
	fmt.Fprintln(out, "Waiting for you to approve in the browser... (Ctrl-C to cancel)")

	key, err := pollForLicenseKey(signalCtx, c, auth)
	if err != nil {
		return err
	}

	// Store via the same path as 'sukko license set' (validate claims, encrypt).
	claims, err := decodeLicenseClaims(key)
	if err != nil {
		return fmt.Errorf("validate issued license key: %w", err)
	}
	enc, err := store.EncryptSecret(key)
	if err != nil {
		return fmt.Errorf("encrypt license key: %w", err)
	}
	ctx.LicenseKeyEnc = enc
	if err := store.Add(ctx); err != nil {
		return fmt.Errorf("save context: %w", err)
	}

	fmt.Fprintf(out, "\nLogged in. Edition: %s, Org: %s, Expires: %s\n",
		capitalizeEdition(claims.Edition), claims.Org, formatExpiry(claims.Exp))
	return nil
}

// pollForLicenseKey polls the token endpoint until the user approves (returns the
// key), denies, or the code expires. It polls immediately (the user may have
// already approved via the prefilled link), then waits the server-advised interval
// between polls, backing off on slow_down and honoring ctx cancellation (Ctrl-C).
func pollForLicenseKey(ctx context.Context, c *client.LicenseDeviceClient, auth *client.DeviceAuthorization) (string, error) {
	interval := max(time.Duration(auth.Interval)*time.Second, minPollInterval)
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	transientFailures := 0

	for {
		// Don't poll a canceled context (Ctrl-C before the first/next poll).
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("login canceled: %w", ctx.Err())
		default:
		}

		tok, err := c.PollToken(ctx, auth.DeviceCode)
		switch {
		case err == nil:
			return tok.LicenseKey, nil
		case errors.Is(err, client.ErrDeviceAuthorizationPending):
			transientFailures = 0
		case errors.Is(err, client.ErrDeviceSlowDown):
			transientFailures = 0
			interval += slowDownIncrement
		case errors.Is(err, client.ErrDeviceRateLimited), errors.Is(err, client.ErrDeviceTransient):
			// A cancel (Ctrl-C) that landed mid-request surfaces here as a transient
			// error; report it as a cancellation, not a reachability failure — even
			// when it coincides with the last tolerated attempt.
			if ctx.Err() != nil {
				return "", fmt.Errorf("login canceled: %w", ctx.Err())
			}
			// A transient blip (429, 5xx, connection reset, cold start) during the
			// human-in-the-loop wait must not abort login — retry a bounded number of
			// consecutive times, then give up.
			transientFailures++
			if transientFailures >= maxConsecutiveTransientFailures {
				return "", fmt.Errorf("could not reach the license service after %d attempts: %w", transientFailures, err)
			}
		case errors.Is(err, client.ErrDeviceExpired):
			return "", errors.New("the code expired before it was approved — run 'sukko license login' again")
		case errors.Is(err, client.ErrDeviceAccessDenied):
			return "", errors.New("the request was denied in the browser")
		default:
			return "", fmt.Errorf("device login failed: %w", err)
		}

		if auth.ExpiresIn > 0 && time.Now().After(deadline) {
			return "", errors.New("the code expired before it was approved — run 'sukko license login' again")
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("login canceled: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}
