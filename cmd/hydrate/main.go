package main

import (
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/pkg/errors"
	"github.com/pressly/hydrate"
)

var (
	flags    = flag.NewFlagSet("hydrate", flag.ExitOnError)
	region   = flags.String("region", "", "AWS region")
	basePath = flags.String("path", "", "base path for AWS SSM Parameter Store parameters")
	format   = flags.String("format", "", "input file format (json, yaml, toml, k8s)")
	debug    = flags.Bool("debug", false, "print debug info to stderr")
	k8s      = flags.Bool("k8s", false, "hydrate Kubernetes Secret/ConfigMap objects' base64-encoded data fields")

	usage = errors.New(`hydrate:

Hydrate values matching "^\$SECRET:" regex with values from AWS SSM Param Store.

usage:
	# hydrate JSON file
		hydrate no-secrets.json > secrets.json

	# hydrate YAML data from stdin
		echo "data: $SECRET:/app/sit1/app_secret_data_key" | hydrate --format=yml - > secret.yml

	# hydrate Kubernetes Secrets/ConfigMap "stringData" and base64-encoded "data" files/values
		hydrate -k8s k8s-secret.yml | kubectl apply -
`)
)

func main() {
	flags.Parse(os.Args[1:])

	if *region == "" {
		*region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if *region == "" {
		log.Fatal(errors.New("hydrate: --region=[us-west-2] or $AWS_DEFAULT_REGION must be provided"))
	}

	args := flags.Args()
	if len(args) != 1 {
		log.Fatal(usage)
	}
	filename := args[0]

	var r io.Reader
	if filename == "-" {
		if *format == "" {
			log.Fatal(errors.New("hydrate: --format=[json|yaml|toml} must be provided when using STDIN"))
		}
		r = os.Stdin
	} else {
		if *format == "" {
			*format = strings.TrimLeft(filepath.Ext(filename), ".")
		}
		f, err := os.Open(filename)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		r = io.Reader(f)
	}

	sess, err := session.NewSession(&aws.Config{
		CredentialsChainVerboseErrors: aws.Bool(true),
		Region:                        region,
	})
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed to create aws session"))
	}

	paramStore := hydrate.ParamStore(ssm.New(sess, aws.NewConfig()), *basePath)
	if *debug {
		paramStore.Debug(true)
	}

	data, err := hydrate.GetData(r, *format)
	if err != nil {
		log.Fatal(err)
	}

	if *k8s {
		if err := paramStore.HydrateK8s(data); err != nil {
			log.Fatal(err)
		}
	} else {
		if err := paramStore.Hydrate(data); err != nil {
			log.Fatal(err)
		}
	}

	if err := hydrate.PrintData(os.Stdout, data, *format); err != nil {
		log.Fatal(err)
	}
}
