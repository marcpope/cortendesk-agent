#!/bin/sh
# Signs and notarizes the macOS agent on the Mac that holds the Developer ID
# key. Run it there, after `make dist`, before `make manifest` (signing
# changes the bytes the manifest hashes):
#
#   tools/sign-macos.sh dist/cortendesk-agent_darwin_universal
#
# A bare binary can't be stapled; Gatekeeper fetches the notarization ticket
# online. The curl installer doesn't quarantine the file, so this matters
# for manual downloads.
set -eu

BIN=${1:?usage: tools/sign-macos.sh <binary>}
IDENTITY=${MACOS_SIGN_IDENTITY:-"Developer ID Application: Marc Pope (Z7GF86P2E2)"}
PROFILE=${MACOS_NOTARY_PROFILE:-flick-notary}

codesign --force --sign "$IDENTITY" --options runtime --timestamp \
    --identifier com.cortendesk.agent "$BIN"
codesign --verify --strict --verbose=2 "$BIN"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
ditto -c -k --keepParent "$BIN" "$tmp/agent.zip"
xcrun notarytool submit "$tmp/agent.zip" --keychain-profile "$PROFILE" --wait | tee "$tmp/notary.log"
grep -q "status: Accepted" "$tmp/notary.log" || { echo "notarization was not accepted" >&2; exit 1; }

shasum -a 256 "$BIN"
