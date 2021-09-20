package action

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

type DeprecateTruncate struct {
	FromDir      string
	BundleImages []string

	Log *logrus.Logger
}

func (d DeprecateTruncate) getBundlesToDeprecate(bundles []declcfg.Bundle) ([]declcfg.Bundle, error) {
	depImages := sets.NewString(d.BundleImages...)
	foundImages := sets.NewString()
	var found []declcfg.Bundle
	for _, b := range bundles {
		if depImages.Has(b.Image) {
			foundImages.Insert(b.Image)
			found = append(found, b)
		}
	}
	notFound := depImages.Difference(foundImages)
	if notFound.Len() > 0 {
		return nil, fmt.Errorf("could not find bundles in the index: %q", strings.Join(notFound.List(), ","))
	}
	return found, nil
}

func (d DeprecateTruncate) Run(ctx context.Context) error {
	// Deprecatetruncate for FBC is just removing the deprecated bundle and its tail.
	// In FBC, there is no requirement that every bundle referenced by a replaces value is in
	// the channel or package, so keeping a deprecated bundle around is unnecessary.
	//
	//   Step 1: Find the olm.bundle blob for the requested deprecation image
	//   Step 2: For each channel in the bundle's package:
	//     - build the replaces chain of entries
	//     - remove each entry from the channel, starting at the
	//       deprecated bundle and ending at the end of the tail
	//     - keep track of all removed entry names
	//   Step 3:
	//     - Search all channels for removed entries
	//     - If a removed entry cannot be found in any channel, remove
	//       the olm.bundle blob for that entry from the catalog

	d.Log.Infof("Loading declarative configs")
	fromCfg, err := declcfg.LoadFS(os.DirFS(d.FromDir))
	if err != nil {
		return fmt.Errorf("load declarative configs: %v", err)
	}

	if _, err := declcfg.ConvertToModel(*fromCfg); err != nil {
		return fmt.Errorf("input catalog is invalid: %v", err)
	}

	depBundles, err := d.getBundlesToDeprecate(fromCfg.Bundles)
	if err != nil {
		return err
	}

	for _, depBundle := range depBundles {
		removedFromChannel := sets.NewString()
		for i, ch := range fromCfg.Channels {
			// We only care about the bundle's package.
			if ch.Package != depBundle.Package {
				continue
			}
			// Build a map of all of our channel entries
			entries := map[string]declcfg.ChannelEntry{}
			for _, e := range ch.Entries {
				entries[e.Name] = e
			}
			// If the deprecated bundle is not in this channel,
			// no changes are necessary here, so continue to the
			// next channel.
			if _, ok := entries[depBundle.Name]; !ok {
				continue
			}

			// Build the replaces chain in this channel.
			chain := map[string]string{}
			for _, e := range ch.Entries {
				chain[e.Name] = e.Replaces
			}

			// Traverse the chain starting at the deprecated bundle, building a set
			// of the entries we need to remove.
			toRemove := sets.NewString()
			for cur := depBundle.Name; cur != ""; cur = chain[cur] {
				toRemove.Insert(cur)
			}
			removedFromChannel = removedFromChannel.Union(toRemove)

			// Remove the tail entries.
			tmpEntries := ch.Entries[:0]
			for _, e := range ch.Entries {
				if !toRemove.Has(e.Name) {
					tmpEntries = append(tmpEntries, e)
				}
			}
			ch.Entries = tmpEntries
			fromCfg.Channels[i] = ch
		}

		// fully remove empty channels
		tmpChannels := fromCfg.Channels[:0]
		for _, ch := range fromCfg.Channels {
			if len(ch.Entries) > 0 {
				tmpChannels = append(tmpChannels, ch)
			}
		}
		fromCfg.Channels = tmpChannels

		presentInOtherChannels := sets.NewString()
		for _, ch := range fromCfg.Channels {
			if ch.Package != depBundle.Package {
				continue
			}
			for _, e := range ch.Entries {
				if removedFromChannel.Has(e.Name) {
					presentInOtherChannels.Insert(e.Name)
				}
			}
		}
		fullyRemoved := removedFromChannel.Difference(presentInOtherChannels)

		tmpBundles := fromCfg.Bundles[:0]
		for _, b := range fromCfg.Bundles {
			if b.Package == depBundle.Package && fullyRemoved.Has(b.Name) {
				continue
			}
			tmpBundles = append(tmpBundles, b)
		}
		fromCfg.Bundles = tmpBundles
	}
	if _, err := declcfg.ConvertToModel(*fromCfg); err != nil {
		return fmt.Errorf("updated file-based catalog is invalid: %v", err)
	}

	d.Log.Infof("Writing updated file-based catalog")
	return writeToFS(*fromCfg, d.FromDir, declcfg.WriteYAML)
}
