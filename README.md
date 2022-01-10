# :exclamation: ARCHIVED :exclamation:

 This repository has been archived and is no longer under active development. The team behind `dcm` abandoned the approach of this tool in favor of using existing imperative `opm` commands followed by a conversion to the file-based catalog format using `opm render` or `opm migrate`

# Declarative Config Migrator (DCM)

`dcm` is a purpose-built command line tool meant to help existing users of `opm` who build sqlite-based index images to gracefully transition to Declarative Config-based index images using imperative workflow commands similar to what exists in `opm`.

## Support

This tool is jointly maintained for internal use by Red Hat's OpenShift OLM team and EXD Container Pipelines team, and is tied to the lifecycle of the sqlite-to-DC operator catalog transition.

This tool tracks the deprecation and EoL timelines of OLM's sqlite support. As of 5/11/2021, the tentative plan is:
- Deprecation in OpenShift 4.9
- EoL in OpenShift 4.11

## Install

### Releases

Binary downloads are available on the [releases](https://github.com/release-engineering/dcm/releases) page.

### From source
```bash
( 
  TMPDIR=$(mktemp -d)
  git clone https://github.com/release-engineering/dcm $TMPDIR
  make -C $TMPDIR install
  rm -rf $TMPDIR
)
```

## Features

The features supported by `dcm` are a subset of the features supported by `opm` that focus on the existing modes that are supported for migration to declarative config. At a high level these features are:

### Migrating an existing index images

To avoid the problem of needing to build a DC index from scratch, `dcm` supports migrating an existing SQLite-based index image to DC and writing out a DC index directory to the local filesystem.

NOTE: Building a new DC index image is out of scope for `dcm`. `opm`'s roadmap includes a feature to help users build an index image from a DC directory.

```
$ dcm migrate -h
Migrate an index image to a declarative config directory

Usage:
  dcm migrate <indexImage> [flags]

  Flags:
    -h, --help                help for migrate
    -d, --output-dir string   Directory in which to migrated index as declarative config (default "index")
```

### Adding bundles

One of the primary uses of `opm` is to add bundles to indices, so this command is carried over to `dcm`. However, it only supports a subset of what the `opm index add` command supports.

- It hardcodes `replaces` mode semantics. The `semver` and `semver-skippatch` modes are not supported. This includes the `replaces` mode behavior of automatically promoting bundles (and bundles in their replaces chain) when they are referenced in the `replaces` field in new channels' bundles.
- It supports the `--overwrite-latest` flag when adding a bundle that already exists in the index and is a channel head in every channel it is a member of.
- It supports adding bundles that use the `olm.substitutesFor` CSV annotation and making the appropriate graph updates to insert them in the correct place.

```
$ dcm add -h
Add a bundle to a declarative config directory

Usage:
  dcm add <dcDir> <bundleImage> [flags]

  Flags:
    -h, --help               help for add
        --overwrite-latest   Allow bundles that are channel heads to be overwritten
```

### Deprecating bundles

There are cases when existing bundles in an index need to be marked as deprecated so that they cannot be installed on a cluster. This is a DC implementation of `opm`'s `deprecatetruncate` subcommand.

```
Deprecate a bundle from a declarative config directory

Usage:
  dcm deprecatetruncate <dcDir> <bundleImage> [flags]

  Flags:
    -h, --help   help for deprecatetruncate
```

