# cloudflare-operator

The goal of cloudflare-operator is to manage Cloudflare DNS records using Kubernetes objects.

cloudflare-operator is built from the ground up to use Kubernetes' API extension system.

## Who is cloudflare-operator for?

cloudflare-operator helps to:

- Manage Cloudflare DNS records using Kubernetes objects
- Keep Cloudflare DNS records up to date
- Update your external IP address on Cloudflare DNS records

## What can I do with cloudflare-operator?

cloudflare-operator is based on a set of Kubernetes API extensions ("custom resources"), which control Cloudflare DNS records.

## Where do I start?

Following [this](https://containeroo.ch/docs/cloudflare-operator) guide will just take a couple of minutes to complete. After installing the cloudflare-operator helm chart and adding some annotation to your ingresses, cloudflare-operator will take care of your Cloudflare DNS records.

## More detail on what’s in cloudflare-operator

Features:

- Add, update and delete Cloudflare DNS records
- Update Cloudflare DNS records if your external IP address changes

## Record ownership and reconciliation

A Cloudflare record belongs to one DNSRecord resource. Adoption requires an exact
name, type, content or structured data, and priority match. Records already claimed
by another DNSRecord are excluded. Different addresses for the same name and type
create separate remote records. Ambiguous identical records must be resolved before
adoption; the operator reports the conflict instead of choosing one.

Ingress and Gateway API resources in the same namespace can share a generated
DNSRecord when their DNS annotations produce identical specifications. Removing one
owner preserves the record for the others. Shared changes take effect once all
remaining owners agree. Generated names include a hostname hash to avoid collisions;
existing generated resources keep their names.

DNSRecord status retains the remote zone ID and Account name. Moving a record to a
new zone or Account removes the previous binding before creating the replacement.
Deleting the Zone resource does not prevent cleanup of records with this stored
identity. Keep the original Account and its token Secret available until cleanup is
complete.

On upgrade, existing records acquire this identity during reconciliation. Allow a
successful sync before changing their zone or account configuration. For an older
record whose original Zone was already removed or changed, restore that Zone or
populate the verified `status.zoneID` and `status.accountName` before deletion. The
operator cannot reconstruct historical identity from a record ID alone.

Intervals must be positive Go durations such as `30s`, `5m`, or `1h30m`. Invalid
annotation intervals fall back to the configured default. An invalid pruning
exclusion stops the entire pruning pass without deleting records. Once a record is
bound, pruning protects its exact remote ID rather than every record with the same
name and type.

The status metrics use `0` for Ready, `1` for Failed, and `2` for Unknown. The supplied
dashboard displays all three states and counts only Failed resources as errors.

## Development checks

Run `make test` for local tests and `make test-integration` for CRD admission and
informer/finalizer checks against a temporary Kubernetes API server. The integration
target downloads pinned local test binaries and needs no cluster or Cloudflare
credentials. Live Cloudflare and Kind end-to-end checks remain separate.

## Disclaimer

This is not an official Cloudflare project. Use at your own risk.

If you encounter any issues, please open an issue on GitHub.

If everything works fine, please send your compliments to Cloudflare.

If everthing does not work fine, please send your complaints to us :D

Cloudflare is a registered trademark of Cloudflare, Inc.
