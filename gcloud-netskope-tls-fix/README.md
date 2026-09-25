# gcloud + Netskope TLS fix

On this machine, `gcloud` fails every authenticated call with SSL errors, even
though `curl` and the `openssl` CLI work fine against the same hosts. This
documents the root cause and the fix.

## Symptom

```
ERROR: (gcloud.iam.service-accounts.list) There was a problem refreshing your
current auth tokens: HTTPSConnectionPool(host='oauth2.googleapis.com', port=443):
Max retries exceeded with url: /token (Caused by SSLError(SSLCertVerificationError(
1, '[SSL: CERTIFICATE_VERIFY_FAILED] certificate verify failed: Basic Constraints
of CA cert not marked critical (_ssl.c:1032)')))
```

`curl https://oauth2.googleapis.com` and `openssl s_client -connect
oauth2.googleapis.com:443 -CAfile <bundle>` both succeed with the exact same
corporate CA bundle. Only Python-based tools (`gcloud`, `gsutil`, anything
using `ssl.create_default_context()`) fail.

## Root cause

This network runs Netskope TLS inspection (a corporate MITM proxy). Netskope's
issued root and per-tenant intermediate CA certificates are not fully RFC 5280
compliant:

- `X509v3 Basic Constraints` is present but **not marked `critical`** (RFC 5280
  §4.2.1.9 requires `critical` on this extension for CA certificates).
- Neither cert carries an `X509v3 Key Usage` extension at all (RFC 5280 wants
  `keyCertSign` asserted on CA certs).

macOS's own TLS stack (Secure Transport, used by `curl` on macOS) tolerates
both gaps. Python 3.13's `ssl` module, linked against OpenSSL 3.6.x, enables
`ssl.VERIFY_X509_STRICT` by default and rejects both — first on the
Basic-Constraints criticality, then (once that's fixed) on the missing
KeyUsage extension.

`gcloud` runs its own isolated Python venv (see `gcloud info` → `Python
Location`), so this only affects `gcloud`/`gsutil`, not other tools.

## Where the certs actually are

Corporate root/intermediate CAs are already in the macOS System keychain
(`security find-certificate -a /Library/Keychains/System.keychain`), just not
in the CA bundle gcloud reads. Relevant ones for Netskope:

- `certadmin` (CN=certadmin, O=Netskope Inc.) — the actual Netskope root, self-signed.
- `ca.rak.goskope.com` — the tenant-specific intermediate Netskope issues per organization.

Plus several `Rakuten Corporate IT/NetAuth *CA*` certs, which validate fine
(their Basic Constraints is already `critical`) — only the two Netskope certs
above are malformed.

## The fix

Two parts:

### 1. Build a combined CA bundle

Export the missing corporate roots from the keychain and append them to a
standard bundle:

```sh
mkdir -p ~/.gcloud-corp-ca
for name in "ca.rak.goskope.com" "Rakuten Corporate IT Root CA" \
  "Rakuten Corporate IT Sites Intermediate CA" "Rakuten Corporate IT Sites CA Tokyo" \
  "Rakuten Corporate NetAuth Intermediate CA" "Rakuten Corporate NetAuth CA TYO" \
  "Rakuten Corporate NetAuth CA UKB" "Rakuten Corporate IT NetAuth CA Tokyo" \
  "Rakuten Corporate IT NetAuth Intermediate CA"; do
  security find-certificate -c "$name" -p /Library/Keychains/System.keychain >> ~/.gcloud-corp-ca/corp-roots.pem
done
security find-certificate -c "certadmin" -p /Library/Keychains/System.keychain > ~/.gcloud-corp-ca/netskope-root.pem
```

(`ca.rak.goskope.com` ends up as one of the entries in `corp-roots.pem` — split
it out to its own file, e.g. `cert_1.pem`, since it needs patching below.)

### 2. Patch the two malformed certs

`patch_critical.py` (in this directory) rebuilds the Netskope root and
intermediate with `Basic Constraints: critical` and a proper `Key Usage:
keyCertSign, cRLSign` extension added. It uses one throwaway keypair to
re-self-sign the root and re-sign the intermediate — **critically, the
intermediate keeps its original public key**, since the real intercepted
leaf certificates (e.g. `oauth2.googleapis.com`) were signed by the real
Netskope intermediate's private key and must still validate against that
same public key. Only the root↔intermediate link (which nothing outside this
bundle depends on) uses the throwaway key.

```sh
python3 patch_critical.py \
  ~/.gcloud-corp-ca/netskope-root.pem ~/.gcloud-corp-ca/netskope-root-patched.pem \
  ~/.gcloud-corp-ca/cert_1.pem ~/.gcloud-corp-ca/cert_1-patched.pem
```

Requires the `cryptography` package. gcloud's own venv already has it —
`~/.config/gcloud/virtenv/bin/python3.13 patch_critical.py ...` — so no
separate install is needed on a machine with gcloud installed.

### 3. Rebuild the bundle with the patched certs and point gcloud at it

```sh
cd ~/.gcloud-corp-ca
cat /etc/ssl/cert.pem cert_2.pem cert_3.pem cert_4.pem cert_5.pem cert_6.pem \
    cert_7.pem cert_8.pem cert_9.pem cert_1-patched.pem netskope-root-patched.pem \
    > combined-ca-bundle.pem

gcloud config set core/custom_ca_certs_file ~/.gcloud-corp-ca/combined-ca-bundle.pem
```

Verify the chain before trusting `gcloud` with it:

```sh
openssl verify -CAfile ~/.gcloud-corp-ca/netskope-root-patched.pem ~/.gcloud-corp-ca/cert_1-patched.pem
# expect: cert_1-patched.pem: OK
```

Then a normal `gcloud auth login` (interactive — needs a browser) followed by
any authenticated call should work.

## What did NOT work

- **Homebrew's default `openssl@3` cert.pem alone** — same criticality error,
  since it doesn't contain the Netskope roots at all (`unable to get issuer
  certificate` once the roots are added but before the criticality fix).
- **Monkeypatching `ssl.create_default_context`** via a `sitecustomize.py` in
  gcloud's venv site-packages — silently never loaded, because Homebrew's base
  Python install already ships its own `sitecustomize.py` earlier on
  `sys.path`, and Python only imports the first module of a given name it
  finds.
- **Monkeypatching via a uniquely-named module + `.pth` file** (avoids the name
  collision above) — the patch *did* load, but `gcloud`'s bundled Python
  doesn't create its SSL contexts through `ssl.create_default_context()` at
  all (it goes through `requests`/`urllib3`'s own context builder), so nothing
  was actually intercepted.
- **Subclassing `ssl.SSLContext` and reassigning `ssl.SSLContext` globally** —
  did intercept construction, but broke `gcloud`'s own bootstrap entirely
  (`gcloud failed to load`) — something in gcloud's startup path depends on
  `ssl.SSLContext` being the exact built-in type, not a subclass. Reverted.
- **First cert-patching attempt**: rebuilt both certs with a fresh throwaway
  key each, and set each cert's `issuer_name` to its own `subject` — correct
  for the self-signed root, wrong for the intermediate (which made it falsely
  self-issued, breaking the chain: `unable to get issuer certificate` even
  though the `Authority Key Identifier`/`Subject Key Identifier` matched by
  value).

## Security notes

- This does not disable certificate verification. Signatures, expiry,
  hostname, and full chain-of-trust checks all still run — only two
  RFC5280-strictness checks (extension criticality, extension presence) are
  relaxed by correcting the cert data itself, not by turning verification off.
- The patched root/intermediate use a throwaway key with no relationship to
  Netskope's real private key. They're trusted locally (added to this
  machine's own `gcloud` CA bundle) purely to complete the chain for local
  validation — they don't grant any capability Netskope's real MITM proxy
  didn't already have on this network.
- `core/custom_ca_certs_file` only affects `gcloud`/`gsutil`'s own Python
  process; it isn't a system-wide trust change.
