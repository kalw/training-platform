// Command training is the all-in-one training-platform binary. One Go
// program, one container image, deployed only on Kubernetes; it bundles
// every server-side training surface as subcommands:
//
//	training serve    run the composed platform (lessons + scoring +
//	                  terminals + optional Docker shim) on one port
//	training shim     run only the Docker-Engine-API -> Kubernetes shim
//	                  (for "play with docker" content)
//	training router   run only the exposed-port router
//	training version  print build info
//
// Course content still runs Docker (DinD sessions) — the k8s-only rule is
// about how the platform is *deployed*, not what learners can do inside a
// session. See training-deployment/K8S-SANDBOX-DESIGN.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kalw/training-platform/internal/auth"
	"github.com/kalw/training-platform/internal/content"
	"github.com/kalw/training-platform/internal/dockershim"
	"github.com/kalw/training-platform/internal/router"
	"github.com/kalw/training-platform/internal/server"
	"github.com/kalw/training-platform/internal/version"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("training: ")

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "shim":
		cmdShim(os.Args[2:])
	case "router":
		cmdRouter(os.Args[2:])
	case "build":
		cmdBuild(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version.String())
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `training — all-in-one training platform (Kubernetes-only deployment)

usage:
  training serve    [flags]   run the composed platform
  training shim     [flags]   run only the Docker-API -> Kubernetes shim
  training router   [flags]   run only the exposed-port router
  training build    [flags]   render Markdown lessons -> HTML + challenges.json
  training version            print build info

run "training <subcommand> -h" for subcommand flags
`)
}

func cmdBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	src := fs.String("src", "lessons", "directory of Markdown lessons")
	out := fs.String("out", "site", "output directory for HTML + challenges.json")
	salt := fs.String("salt", envOr("CTFD_SALT", "insecure-default-salt"), "scoring salt (must match the deployment)")
	_ = fs.Parse(args)

	n, challenges, err := content.BuildDir(*src, *out, *salt)
	if err != nil {
		log.Fatalf("build: %v", err)
	}
	log.Printf("rendered %d lesson(s), %d challenge(s) -> %s (challenges.json included)", n, len(challenges), *out)
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	lessonsDir := fs.String("lessons-dir", envOr("LESSONS_DIR", ""), "static lesson site directory")
	nsPrefix := fs.String("session-namespace-prefix", envOr("SESSION_NS_PREFIX", "session-"), "namespace prefix for sessions")
	ttl := fs.Duration("session-ttl", 4*time.Hour, "maximum session lifetime (hard cap)")
	idleTTL := fs.Duration("session-idle-ttl", 10*time.Minute, "how long instance pods survive without a page keepalive (closed/hidden tab); negative disables idle GC")
	dind := fs.String("instance-image", envOr("INSTANCE_IMAGE", "ghcr.io/kalw/training-console-pwd:dind"), "default session instance image")
	termNS := fs.String("terminal-namespace", envOr("TERMINAL_NS", "training-sessions"), "namespace holding instance pods for terminals")
	enableShim := fs.Bool("enable-shim", envBool("ENABLE_SHIM", true), "mount the Docker-API shim under /docker/")
	shimNS := fs.String("shim-namespace", envOr("SHIM_NS", "training-sessions"), "namespace the Docker shim materializes containers into")
	salt := fs.String("salt", os.Getenv("CTFD_SALT"), "scoring salt (must match the lessons build)")
	challengesFile := fs.String("challenges-file", envOr("CHALLENGES_FILE", ""), "JSON challenges file to seed scoring at boot")
	routerHost := fs.String("router-host", envOr("ROUTER_HOST", ""), "public suffix for exposed-port links (e.g. direct.training.example.com)")
	baseURL := fs.String("base-url", envOr("BASE_URL", ""), "external origin, for social-login redirect URLs")
	_ = fs.Parse(args)

	h, eng, err := server.New(server.Config{
		LessonsDir:             *lessonsDir,
		SessionNamespacePrefix: *nsPrefix,
		SessionTTL:             *ttl,
		SessionIdleTTL:         *idleTTL,
		DefaultInstanceImage:   *dind,
		TerminalNamespace:      *termNS,
		EnableShim:             *enableShim,
		ShimNamespace:          *shimNS,
		Salt:                   *salt,
		ChallengesFile:         *challengesFile,
		RouterHost:             *routerHost,
		// Social login: providers enable themselves when their client
		// id/secret env vars are present. See internal/auth.
		Auth: auth.Options{
			BaseURL:            *baseURL,
			Secret:             os.Getenv("AUTH_SECRET"),
			Secure:             envBool("AUTH_COOKIE_SECURE", true),
			GitHubClientID:     os.Getenv("GITHUB_CLIENT_ID"),
			GitHubClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
			GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
			GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		},
	})
	if err != nil {
		log.Fatalf("serve: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go server.RunGC(ctx, eng, *termNS, time.Minute)

	srv := &http.Server{Addr: *addr, Handler: h}
	go func() {
		<-ctx.Done()
		sd, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sd)
	}()
	log.Printf("serving on %s (shim=%v)", *addr, *enableShim)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func cmdShim(args []string) {
	fs := flag.NewFlagSet("shim", flag.ExitOnError)
	addr := fs.String("addr", ":2375", "listen address (Docker Engine API)")
	ns := fs.String("namespace", envOr("SHIM_NS", "training-sessions"), "target namespace")
	_ = fs.Parse(args)
	if err := dockershim.Serve(*addr, *ns); err != nil {
		log.Fatalf("shim: %v", err)
	}
}

func cmdRouter(args []string) {
	fs := flag.NewFlagSet("router", flag.ExitOnError)
	addr := fs.String("addr", ":8090", "listen address")
	defPort := fs.Int("default-port", 80, "port to use when the host encodes none")
	_ = fs.Parse(args)

	proxy := router.NewHTTPProxy(*defPort)
	log.Printf("router listening on %s", *addr)
	if err := http.ListenAndServe(*addr, proxy); err != nil {
		log.Fatalf("router: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	switch os.Getenv(key) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	default:
		return def
	}
}
