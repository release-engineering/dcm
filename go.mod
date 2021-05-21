module github.com/joelanford/dcm

go 1.16

replace github.com/operator-framework/operator-registry => github.com/joelanford/operator-registry v1.12.2-0.20210520155234-4ce23698cf9c

require (
	github.com/Shopify/logrus-bugsnag v0.0.0-20171204204709-577dee27f20d // indirect
	github.com/blang/semver/v4 v4.0.0
	github.com/bshuster-repo/logrus-logstash-hook v1.0.0 // indirect
	github.com/mattn/go-sqlite3 v1.14.7 // indirect
	github.com/operator-framework/operator-registry v0.0.0-00010101000000-000000000000
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/cobra v1.1.1
	k8s.io/apimachinery v0.20.6
	rsc.io/letsencrypt v0.0.3 // indirect
)
