package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/projectatomic/buildah"
	buildahcli "github.com/projectatomic/buildah/pkg/cli"
	"github.com/projectatomic/buildah/pkg/parse"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var (
	runFlags = []cli.Flag{
		cli.StringFlag{
			Name:  "hostname",
			Usage: "set the hostname inside of the container",
		},
		cli.StringFlag{
			Name:  "runtime",
			Usage: "`path` to an alternate runtime",
			Value: buildah.DefaultRuntime,
		},
		cli.StringSliceFlag{
			Name:  "runtime-flag",
			Usage: "add global flags for the container runtime",
		},
		cli.StringSliceFlag{
			Name:  "security-opt",
			Usage: "security options (default [])",
		},
		cli.BoolFlag{
			Name:  "t, tty, terminal",
			Usage: "allocate a pseudo-TTY in the container",
		},
		cli.StringSliceFlag{
			Name:  "volume, v",
			Usage: "bind mount a host location into the container while running the command",
		},
	}
	runDescription = "Runs a specified command using the container's root filesystem as a root\n   filesystem, using configuration settings inherited from the container's\n   image or as specified using previous calls to the config command"
	runCommand     = cli.Command{
		Name:           "run",
		Usage:          "Run a command inside of the container",
		Description:    runDescription,
		Flags:          append(append(runFlags, userFlags...), buildahcli.NamespaceFlags...),
		Action:         runCmd,
		ArgsUsage:      "CONTAINER-NAME-OR-ID COMMAND [ARGS [...]]",
		SkipArgReorder: true,
	}
)

func runCmd(c *cli.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return errors.Errorf("container ID must be specified")
	}
	name := args[0]
	if err := parse.ValidateFlags(c, append(append(runFlags, userFlags...), buildahcli.NamespaceFlags...)); err != nil {
		return err
	}

	args = args.Tail()
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	if len(args) == 0 {
		return errors.Errorf("command must be specified")
	}

	store, err := getStore(c)
	if err != nil {
		return err
	}

	builder, err := openBuilder(getContext(), store, name)
	if err != nil {
		return errors.Wrapf(err, "error reading build container %q", name)
	}

	runtimeFlags := []string{}
	for _, arg := range c.StringSlice("runtime-flag") {
		runtimeFlags = append(runtimeFlags, "--"+arg)
	}

	user := parseUserOptions(c)
	namespaceOptions, networkPolicy, err := parseNamespaceOptions(c)
	if err != nil {
		return errors.Wrapf(err, "error parsing namespace-related options")
	}

	options := buildah.RunOptions{
		Hostname:         c.String("hostname"),
		Runtime:          c.String("runtime"),
		Args:             runtimeFlags,
		User:             user,
		NamespaceOptions: namespaceOptions,
		ConfigureNetwork: networkPolicy,
		CNIPluginPath:    c.String("cni-plugin-path"),
		CNIConfigDir:     c.String("cni-config-dir"),
	}

	if c.IsSet("tty") {
		if c.Bool("tty") {
			options.Terminal = buildah.WithTerminal
		} else {
			options.Terminal = buildah.WithoutTerminal
		}
	}

	// validate volume paths
	if err := parse.ParseVolumes(c.StringSlice("volume")); err != nil {
		return err
	}

	for _, volumeSpec := range c.StringSlice("volume") {
		volSpec := strings.Split(volumeSpec, ":")
		if len(volSpec) >= 2 {
			mountOptions := "bind"
			if len(volSpec) >= 3 {
				mountOptions = mountOptions + "," + volSpec[2]
			}
			mountOpts := strings.Split(mountOptions, ",")
			mount := specs.Mount{
				Source:      volSpec[0],
				Destination: volSpec[1],
				Type:        "bind",
				Options:     mountOpts,
			}
			options.Mounts = append(options.Mounts, mount)
		}
	}
	runerr := builder.Run(args, options)
	if runerr != nil {
		logrus.Debugf("error running %v in container %q: %v", args, builder.Container, runerr)
	}
	if ee, ok := runerr.(*exec.ExitError); ok {
		if w, ok := ee.Sys().(syscall.WaitStatus); ok {
			os.Exit(w.ExitStatus())
		}
	}
	return runerr
}
