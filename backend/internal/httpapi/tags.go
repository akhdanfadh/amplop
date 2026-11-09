package httpapi

import "github.com/danielgtaylor/huma/v2"

// Each operation lists one of these tags, which the API reference groups it
// under.
const (
	tagSetup             = "Setup"
	tagSessions          = "Sessions"
	tagTwoFactor         = "Two-Factor Authentication"
	tagEmailVerification = "Email Verification"
	tagPasswordReset     = "Password Reset"
	tagRegistration      = "Registration"
	tagMembers           = "Members"
	tagSystemEmail       = "System Email"
	tagConnectedAccounts = "Connected Accounts"
	tagRedirects         = "Redirects"
	tagHealth            = "Health"
	tagProbe             = "Probe"
)

// apiTags returns every tag an operation may list, each with what its
// operations do, in the order the OpenAPI document lists them.
func apiTags() []*huma.Tag {
	return []*huma.Tag{
		{Name: tagSetup, Description: "Create the first administrator of a new instance."},
		{Name: tagSessions, Description: "Sign in and out, and see or end the signed-in account's sessions."},
		{Name: tagTwoFactor, Description: "Enroll an authenticator, replace backup codes, or turn two-factor authentication off."},
		{Name: tagEmailVerification, Description: "Verify the signed-in account's email address with an emailed link."},
		{Name: tagPasswordReset, Description: "Set a forgotten password with an emailed link."},
		{Name: tagRegistration, Description: "Open or close registration, and create member accounts with emailed links."},
		{Name: tagMembers, Description: "List, deactivate, and reactivate accounts. Administrators only."},
		{Name: tagSystemEmail, Description: "The instance's own sender of links and notices. Administrators only."},
		{Name: tagConnectedAccounts, Description: "Connect, list, reconnect, and disconnect the signed-in account's Gmail accounts."},
		{Name: tagRedirects, Description: "Addresses another site sends the browser to, e.g. Google after its consent page. Clients never call them; the web app's generated client leaves them out."},
		{Name: tagHealth, Description: "Whether the process answers requests."},
		{Name: tagProbe, Description: "An example feature that exercises the whole stack. It goes before the first release."},
	}
}
