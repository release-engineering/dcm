package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/blang/semver"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/property"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	libsemver "github.com/operator-framework/operator-registry/pkg/lib/semver"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

func nullLogger() *logrus.Entry {
	logger := logrus.New()
	logger.SetOutput(ioutil.Discard)
	return logrus.NewEntry(logger)
}

type bundleProps struct {
	*declcfg.Bundle
	property.Properties
}

func newBundleProps(b *declcfg.Bundle) (*bundleProps, error) {
	props, err := property.Parse(b.Properties)
	if err != nil {
		return nil, fmt.Errorf("parse properties for bundle %q: %v", b.Name, err)
	}
	bp := &bundleProps{Bundle: b, Properties: *props}
	return bp, nil
}

func writeCfg(cfg declcfg.DeclarativeConfig, dir string) error {
	f, err := os.Create(filepath.Join(dir, "index.yaml"))
	if err != nil {
		return err
	}
	defer f.Close()
	return declcfg.WriteYAML(cfg, f)
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

func destroyRegistry(reg image.Registry, log logrus.Logger) {
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

func getHeads(bundleMap map[string]bundleProps) (map[string]bundleProps, error) {
	inChannel := map[string]sets.String{}
	replacedInChannel := map[string]sets.String{}
	skipped := sets.NewString()
	for _, b := range bundleMap {
		replaces := map[string]struct{}{}
		for _, ch := range b.Channels {
			replaces[ch.Replaces] = struct{}{}

			in, ok := inChannel[ch.Name]
			if !ok {
				in = sets.NewString()
			}
			in.Insert(b.Name)
			inChannel[ch.Name] = in

			rep, ok := replacedInChannel[ch.Name]
			if !ok {
				rep = sets.NewString()
			}
			rep.Insert(ch.Replaces)
			replacedInChannel[ch.Name] = rep
		}
		for _, skip := range b.Skips {
			skipped.Insert(string(skip))
		}
		if len(replaces) > 1 {
			return nil, fmt.Errorf("bundle %q has multiple replaces: channel-specific replaces not supported", b.Name)
		}
	}

	heads := map[string]bundleProps{}
	for name, in := range inChannel {
		replaced := replacedInChannel[name]
		chHeads := in.Difference(replaced).Difference(skipped)
		for _, h := range chHeads.List() {
			heads[h] = bundleMap[h]
		}
	}
	return heads, nil
}

func removeReplacesFor(bundleMap map[string]bundleProps, name string) error {
	for _, b := range bundleMap {
		for i, p := range b.Bundle.Properties {
			if p.Type == "olm.channel" {
				var ch property.Channel
				if err := json.Unmarshal(p.Value, &ch); err != nil {
					return fmt.Errorf("parse channel propertry for bundle %q", b.Name)
				}
				if ch.Replaces == name {
					b.Bundle.Properties[i] = property.MustBuildChannel(ch.Name, "")
				}
			}
		}
	}
	return nil
}

func getDefaultChannel(ctx context.Context, reg image.Registry, bundleMap map[string]bundleProps) (string, error) {
	type bundleVersion struct {
		version semver.Version
		count   int
	}

	var (
		pkgName    string
		defaultCh  string
		maxVersion bundleVersion
	)

	heads := map[string]bundleVersion{}
	for _, b := range bundleMap {
		if pkgName != "" && pkgName != b.Package {
			return "", fmt.Errorf("more than one package in input")
		}
		pkgName = b.Package

		if len(b.Packages) != 1 {
			return "", fmt.Errorf("bundle %q has %d olm.package properties, expected 1", b.Name, len(b.Packages))
		}
		version, err := semver.Parse(b.Packages[0].Version)
		if err != nil {
			return "", fmt.Errorf("failed to parse version %q for bundle %q", b.Packages[0].Version, b.Name)
		}
		current := bundleVersion{version: version, count: 1}

		for _, channel := range b.Channels {
			head, ok := heads[channel.Name]
			if !ok {
				heads[channel.Name] = current
				continue
			}

			c, err := libsemver.BuildIdCompare(current.version, head.version)
			if err != nil {
				return "", fmt.Errorf("compare versions: %v", err)
			}
			switch {
			case c < 0:
				continue
			case c == 0:
				// We have a duplicate version, add the count
				current.count += head.count
			}
			// Current >= head
			heads[channel.Name] = current
		}

		// Set max if bundle is greater
		c, err := libsemver.BuildIdCompare(current.version, maxVersion.version)
		if err != nil {
			return "", fmt.Errorf("compare versions: %v", err)
		}
		switch {
		case c < 0:
			continue
		case c == 0:
			current.count += maxVersion.count
		}

		// Current >= maxVersion
		//  - Get the default channel for the bundle
		//  - If it's set, update the default channel we'll return for the package.
		if err := reg.Pull(ctx, image.SimpleReference(b.Image)); err != nil {
			return "", fmt.Errorf("pull image %q: %v", b.Image, err)
		}
		labels, err := reg.Labels(ctx, image.SimpleReference(b.Image))
		if err != nil {
			return "", fmt.Errorf("get image labels for %q: %v", b.Image, err)
		}
		bundleDefChannel := labels[bundle.ChannelDefaultLabel]
		if bundleDefChannel != "" {
			maxVersion = current
			defaultCh = bundleDefChannel
		}
	}

	if maxVersion.count > 1 {
		return "", fmt.Errorf("more than one bundle with maximum version %s", maxVersion.version)
	}
	if defaultCh == "" {
		return "", fmt.Errorf("unable to determine default channel among channel heads: %+v", heads)
	}
	return defaultCh, nil
}

func addSubstitutesFor(bundleMap map[string]bundleProps, bundle bundleProps) error {
	subsForMap, err := buildSubsForMap(bundleMap)
	if err != nil {
		return err
	}

	substitutesFor := getSubstitutesFor(bundle)
	if substitutesFor != "" {
		if len(bundle.Packages) != 1 {
			return fmt.Errorf("bundle %q has %d %q properties, expected 1", bundle.Name, len(bundle.Packages), property.TypePackage)
		}
		currentSubstitutionVersion, err := semver.Parse(bundle.Packages[0].Version)
		if err != nil {
			return err
		}

		// Check if any other bundle substitutes for the same bundle
		otherSubstitutions := subsForMap[substitutesFor]
		for len(otherSubstitutions) > 0 {
			otherSubstitution := otherSubstitutions[0]
			otherSubstitutions = otherSubstitutions[1:]

			if otherSubstitution.Name != bundle.Name {
				if len(otherSubstitution.Packages) != 1 {
					return fmt.Errorf("bundle %q has %d %q properties, expected 1", otherSubstitution.Name, len(otherSubstitution.Packages), property.TypePackage)
				}
				otherSubstitutionVersion, err := semver.Parse(otherSubstitution.Packages[0].Version)
				if err != nil {
					return err
				}

				// Compare versions
				c, err := libsemver.BuildIdCompare(otherSubstitutionVersion, currentSubstitutionVersion)
				if err != nil {
					return err
				}
				if c < 0 {
					// Update the currentSubstitution substitutesFor to point to otherSubstitution
					// since it is latest
					bundle.SubstitutesFors = []property.SubstitutesFor{property.SubstitutesFor(otherSubstitution.Name)}
					for pi, p := range bundle.Bundle.Properties {
						if p.Type == property.TypeSubstitutesFor {
							bundle.Bundle.Properties[pi] = property.MustBuildSubstitutesFor(otherSubstitution.Name)
						}
					}
					bundleMap[bundle.Name] = bundle
					moreSubstitutions := subsForMap[otherSubstitution.Name]
					otherSubstitutions = append(otherSubstitutions, moreSubstitutions...)
				} else if c > 0 {
					// Update the otherSubstitution's substitutesFor to point to csvName
					// Since it is the latest
					otherSubstitution.SubstitutesFors = []property.SubstitutesFor{property.SubstitutesFor(bundle.Name)}
					for pi, p := range otherSubstitution.Bundle.Properties {
						if p.Type == property.TypeSubstitutesFor {
							otherSubstitution.Bundle.Properties[pi] = property.MustBuildSubstitutesFor(bundle.Name)
						}
					}
					bundleMap[otherSubstitution.Name] = otherSubstitution

					// Update the otherSubstitution's skips to include csvName and its skips
					skips := append(bundle.Skips, property.Skips(bundle.Name))
					otherSubSkipsMap := map[property.Skips]struct{}{}
					for _, s := range otherSubstitution.Skips {
						otherSubSkipsMap[s] = struct{}{}
					}
					for _, s := range skips {
						if _, ok := otherSubSkipsMap[s]; !ok {
							otherSubstitution.Skips = append(otherSubstitution.Skips, s)
							otherSubstitution.Bundle.Properties = append(otherSubstitution.Bundle.Properties, property.MustBuildSkips(string(s)))
						}
					}

					moreSubstitutions := subsForMap[bundle.Name]
					if len(moreSubstitutions) > 1 {
						return fmt.Errorf("programmer error: more than one substitution pointing to %s", bundle.Name)
					}
				} else {
					// the versions are equal
					return fmt.Errorf("cannot determine latest substitution because of duplicate versions")
				}
			}
		}
	}

	// Rebuild subForMap
	subsForLinear, err := buildSubsForLinear(bundleMap)
	if err != nil {
		return err
	}

	// Get latest substitutesFor value of the current bundle
	substitutesFor = getSubstitutesFor(bundle)
	if substitutesFor != "" {
		// Update any replaces that reference the substituted-for bundle
		for _, b := range bundleMap {
			for chi, ch := range b.Channels {
				if ch.Replaces == substitutesFor {
					b.Channels[chi].Replaces = bundle.Name

					chCount := 0
					for pi, p := range b.Bundle.Properties {
						if p.Type == property.TypeChannel && chCount == chi {
							b.Bundle.Properties[pi] = property.MustBuildChannel(ch.Name, bundle.Name)
							chCount++
						}
					}
				}
			}
		}
	}

	// If the substituted-for of the current bundle substitutes for another bundle
	// it should also be added to the skips of the substitutesFor bundle
	for substitutesFor != "" {
		bundle.Skips = append(bundle.Skips, property.Skips(substitutesFor))
		substitutesFor = getSubstitutesFor(bundleMap[substitutesFor])
	}

	// If the substitution (or substitution of substitution) is added before the
	// substituted for bundle, (i.e. the bundle being added is substituted for by
	// another bundle) then transfer the skips from the substitutedFor bundle (this
	// bundle) over to the substitution's skips
	substitutesForBundle, ok := subsForLinear[bundle.Name]
	for ok {
		// TODO(joelanford): refactor into a function because this is used in a couple of places.
		skips := append(bundle.Skips, property.Skips(bundle.Name))
		subSkipMap := map[property.Skips]struct{}{}
		for _, s := range substitutesForBundle.Skips {
			subSkipMap[s] = struct{}{}
		}
		for _, s := range skips {
			if _, ok := subSkipMap[s]; !ok {
				substitutesForBundle.Skips = append(substitutesForBundle.Skips, s)
				substitutesForBundle.Bundle.Properties = append(substitutesForBundle.Bundle.Properties, property.MustBuildSkips(string(s)))
			}
		}
		substitutesForBundle, ok = subsForLinear[substitutesForBundle.Name]
	}

	// Bundles that skip a bundle that is substituted for
	// should also skip the substituted-for bundle
	for _, b := range bundleMap {
		if len(b.Skips) != 0 {
			substitutesSkips := make(map[property.Skips]struct{})
			skipsOverwrite := []property.Skips{}
			for _, skip := range b.Skips {
				substitutesSkips[skip] = struct{}{}
				substitutesForBundle, ok := subsForLinear[string(skip)]
				for ok {
					// consume the slice of substitutions
					substitutesFor = substitutesForBundle.Name
					// shouldn't skip yourself
					if substitutesFor == b.Name {
						break
					}

					substitutesSkips[property.Skips(substitutesFor)] = struct{}{}
					substitutesForBundle, ok = subsForLinear[substitutesFor]
				}
			}
			for s := range substitutesSkips {
				skipsOverwrite = append(skipsOverwrite, s)
				b.Bundle.Properties = append(b.Bundle.Properties, property.MustBuildSkips(string(s)))
			}
			b.Skips = skipsOverwrite
		}
	}

	// If the bundle being added replaces a bundle that is substituted for
	// (for example it was the previous head of the channel), change
	// the replaces to the substituted-for bundle
	replacesSet := map[string]struct{}{}
	replaces := ""
	for _, ch := range bundle.Channels {
		replacesSet[ch.Replaces] = struct{}{}
		replaces = ch.Replaces
	}
	if len(replacesSet) > 1 {
		return fmt.Errorf("bundle %q can only have 1 replaces value, found %d", len(replacesSet))
	}
	if replaces != "" {
		substitutesForBundle, ok := subsForLinear[replaces]
		for ok {
			// update the replaces to a newer substitution
			replaces = substitutesForBundle.Name
			// try to get the substitution of the substitution
			substitutesForBundle, ok = subsForLinear[replaces]
		}
	}

	// update channel in bundle
	for i := range bundle.Channels {
		bundle.Channels[i].Replaces = replaces
	}
	chCount := 0
	for pi, p := range bundle.Bundle.Properties {
		if p.Type == property.TypeChannel {
			bundle.Bundle.Properties[pi] = property.MustBuildChannel(bundle.Channels[chCount].Name, replaces)
			chCount++
		}
	}

	for _, s := range bundle.Skips {
		bundle.Bundle.Properties = append(bundle.Bundle.Properties, property.MustBuildSkips(string(s)))
	}
	return nil
}

func buildSubsForMap(bundleMap map[string]bundleProps) (map[string][]bundleProps, error) {
	subsForMap := map[string][]bundleProps{}
	for _, b := range bundleMap {
		if len(b.Properties.SubstitutesFors) > 1 {
			return nil, fmt.Errorf("bundle %q has %d %q properties, expected no more than 1", b.Name, len(b.Properties.SubstitutesFors), property.TypeSubstitutesFor)
		}
		if len(b.Properties.SubstitutesFors) == 1 {
			subsFor := string(b.Properties.SubstitutesFors[0])
			if subsFor != "" {
				subsForMap[subsFor] = append(subsForMap[subsFor], b)
			}
		}
	}
	return subsForMap, nil
}

func buildSubsForLinear(bundleMap map[string]bundleProps) (map[string]bundleProps, error) {
	subsForMap, err := buildSubsForMap(bundleMap)
	if err != nil {
		return nil, err
	}
	linear := map[string]bundleProps{}
	for k, v := range subsForMap {
		if len(v) != 1 {
			return nil, fmt.Errorf("programmer error: expected exactly one substitution pointing to %q", k)
		}
		linear[k] = v[0]
	}
	return linear, nil
}

func getSubstitutesFor(b bundleProps) string {
	if len(b.Properties.SubstitutesFors) == 1 {
		return string(b.Properties.SubstitutesFors[0])
	}
	return ""
}
