package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/operator-framework/operator-registry/public/declcfg"
	"github.com/operator-framework/operator-registry/public/property"
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

func defaultChannel(ctx context.Context, reg image.Registry, bundleMap map[string]bundleProps) (string, error) {
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

			c := current.version.Compare(head.version)
			if c < 0 {
				continue
			}
			if c == 0 {
				// We have a duplicate version, add the count
				current.count += head.count
			}

			// Current >= head
			heads[channel.Name] = current
		}

		// Set max if bundle is greater
		c := current.version.Compare(maxVersion.version)
		if c < 0 {
			continue
		}
		if c == 0 {
			current.count += maxVersion.count
		}

		if err := reg.Pull(ctx, image.SimpleReference(b.Image)); err != nil {
			return "", fmt.Errorf("pull image %q: %v", b.Image, err)
		}
		labels, err := reg.Labels(ctx, image.SimpleReference(b.Image))
		if err != nil {
			return "", fmt.Errorf("get image labels for %q: %v", b.Image, err)
		}
		bundleDefChannel := labels[bundle.ChannelDefaultLabel]
		if bundleDefChannel != "" {
			// Current >= maxVersion
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
