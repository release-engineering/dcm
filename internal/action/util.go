package action

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"sort"

	"github.com/blang/semver"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	libsemver "github.com/operator-framework/operator-registry/pkg/lib/semver"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

func nullLogger() *logrus.Entry {
	logger := logrus.New()
	logger.SetOutput(ioutil.Discard)
	return logrus.NewEntry(logger)
}

func newRegistry() (image.Registry, error) {
	regCacheDir, err := os.MkdirTemp("", "dcm-cache-")
	if err != nil {
		return nil, err
	}
	return containerdregistry.NewRegistry(
		containerdregistry.WithCacheDir(regCacheDir),
		containerdregistry.WithLog(nullLogger()),
	)
}

func destroyRegistry(reg image.Registry, log *logrus.Logger) {
	if err := reg.Destroy(); err != nil {
		log.Warnf("destroy temporary image registry: %v", err)
	}
}

func ensureDir(dir string) error {
	s, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0777); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !s.IsDir() {
		return fmt.Errorf("path %q is not a directory", dir)
	}
	return nil
}

func updateFBCPackage(fbc *declcfg.DeclarativeConfig, pkgOut declcfg.DeclarativeConfig) {
	if len(pkgOut.Packages) != 1 {
		panic("function precondition not met: expected exactly one output package")
	}

	packageName := pkgOut.Packages[0].Name

	tmpPkgs := fbc.Packages[:0]
	for _, p := range fbc.Packages {
		if p.Name != packageName {
			tmpPkgs = append(tmpPkgs, p)
		}
	}
	fbc.Packages = tmpPkgs

	tmpChannels := fbc.Channels[:0]
	for _, c := range fbc.Channels {
		if c.Package != packageName {
			tmpChannels = append(tmpChannels, c)
		}
	}
	fbc.Channels = tmpChannels

	tmpBundles := fbc.Bundles[:0]
	for _, b := range fbc.Bundles {
		if b.Package != packageName {
			tmpBundles = append(tmpBundles, b)
		}
	}
	fbc.Bundles = tmpBundles

	fbc.Packages = append(fbc.Packages, pkgOut.Packages...)
	fbc.Channels = append(fbc.Channels, pkgOut.Channels...)
	fbc.Bundles = append(fbc.Bundles, pkgOut.Bundles...)
}

func getSubsChains(bundles []*bundle) ([]string, map[string]string, error) {
	// Keep track of what supersedes what. The key is the name of the old
	// bundle and the value is a slice of everything that explicitly supercedes
	// the key.
	supersededBy := map[string][]string{}
	versions := map[string]semver.Version{}
	for _, b := range bundles {
		supersededBy[b.SubstitutesFor] = append(supersededBy[b.SubstitutesFor], b.Name)
		versions[b.Name] = b.Version
	}
	// Chain is a flattened version of supersededBy that forms a linear chain of
	// substitutions from the original bundle to the latest substitution, ordered
	// by buildID-aware semver.
	chain := map[string]string{}
	type key struct {
		a, b string
	}
	for old, subs := range supersededBy {
		comps := map[key]int{}
		for i, a := range subs[:len(subs)-1] {
			for _, b := range subs[i+1:] {
				v, err := libsemver.BuildIdCompare(versions[a], versions[b])
				if err != nil {
					return nil, nil, fmt.Errorf("build id comparison between %q and %q failed: %v", versions[a], versions[b], err)
				}
				comps[key{a, b}] = v
			}
		}
		sort.Slice(subs, func(i, j int) bool {
			return comps[key{subs[i], subs[j]}] < 0
		})
		chain[old] = subs[0]
		for i := range subs[1:] {
			chain[subs[i-1]] = subs[i]
		}
	}

	froms := sets.NewString()
	tos := sets.NewString()
	for k, v := range chain {
		froms.Insert(k)
		tos.Insert(v)
	}
	originals := froms.Difference(tos)
	return originals.List(), chain, nil
}

func addSubsFor(cfg *declcfg.DeclarativeConfig, orig string, sub string) {
	// Rules:
	//  - sub entry skips orig entry
	//  - orig entry's outgoing edges MOVED to sub entry
	//  - orig entry's incoming replaces edges COPIED to sub entry
	//  - orig entry's incoming skips edges COPIED to sub entry
	//  - orig entry's incoming replaces edges CHANGED to skips

	for i, ch := range cfg.Channels {
		// sub entry skips orig entry
		subEntry := declcfg.ChannelEntry{Name: sub, Skips: []string{orig}}
		for j, e := range ch.Entries {
			if e.Name == orig {
				// orig entry's outgoing edges MOVED to sub entry
				subEntry.Replaces = e.Replaces
				subEntry.Skips = sets.NewString(append(subEntry.Skips, e.Skips...)...).List()
				subEntry.SkipRange = e.SkipRange

				e.Replaces = ""
				e.Skips = nil
				e.SkipRange = ""

				// Add subEntry to the channel
				cfg.Channels[i].Entries = append(cfg.Channels[i].Entries, subEntry)
			}
			if sets.NewString(e.Skips...).Has(orig) {
				// orig entry's incoming skips edges COPIED to sub entry
				e.Skips = append(e.Skips, sub)
			}
			if e.Replaces == orig {
				// orig entry's incoming replaces edges COPIED to sub entry
				e.Replaces = sub

				// orig entry's incoming replaces edges CHANGED to skips
				e.Skips = append(e.Skips, orig)
			}
			cfg.Channels[i].Entries[j] = e
		}
	}
}
