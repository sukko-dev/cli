package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/sukko-dev/cli/client"
	clicontext "github.com/sukko-dev/cli/context"
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
	claims, err := fetchAndStoreLicense(cmd.Context(), cmd.OutOrStdout(), store, ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\nLogged in. Edition: %s, Org: %s, Expires: %s\n",
		capitalizeEdition(claims.Edition), claims.Org, formatExpiry(claims.Exp))
	return nil
}

// fetchAndStoreLicense runs the device-login flow against the license service and stores the
// issued key (encrypted) into cliCtx — the same slot 'sukko license set' uses. Shared by
// 'sukko license login' and 'sukko init'. Progress is written to out; Ctrl-C / SIGTERM cancels
// the wait cleanly (nothing is stored). Returns the decoded claims on success.
func fetchAndStoreLicense(parent context.Context, out io.Writer, store *clicontext.Store, cliCtx *clicontext.Context) (*licenseClaims, error) {
	signalCtx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	c := client.NewLicenseDeviceClient(licenseAPIURL())
	auth, err := c.Authorize(signalCtx)
	if err != nil {
		return nil, fmt.Errorf("start device login: %w", err)
	}

	fmt.Fprintf(out, "To connect this device, open:\n\n    %s\n\nand enter the code:  %s\n\n", auth.VerificationURI, auth.UserCode)
	if auth.VerificationURIComplete != "" {
		fmt.Fprintf(out, "Or open this link, which fills the code in for you:\n    %s\n\n", auth.VerificationURIComplete)
	}
	fmt.Fprintln(out, "Waiting for you to approve in the browser... (Ctrl-C to cancel)")

	key, err := pollForLicenseKey(signalCtx, c, auth)
	if err != nil {
		return nil, err
	}

	// Store via the same path as 'sukko license set' (validate claims, encrypt).
	claims, err := decodeLicenseClaims(key)
	if err != nil {
		return nil, fmt.Errorf("validate issued license key: %w", err)
	}
	enc, err := store.EncryptSecret(key)
	if err != nil {
		return nil, fmt.Errorf("encrypt license key: %w", err)
	}
	cliCtx.LicenseKeyEnc = enc
	if err := store.Add(cliCtx); err != nil {
		return nil, fmt.Errorf("save context: %w", err)
	}
	return claims, nil
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
