package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/yetone/magpie/internal/fridaybridge"
)

// fridayBridgeCmd runs magpie's own Friday host bridge: a loopback server
// at FRIDAY_BIND_ADDR (127.0.0.1:18082 by default) that forwards the
// Friday Adapter's endpoints to FRIDAY_REMOTE_HOST, behind the shared
// FRIDAY_BRIDGE_TOKEN. It reads no providers or settings, so it runs
// before the setup the other commands need.
func fridayBridgeCmd(args []string) error {
	cfg, err := fridaybridge.ConfigFromEnv()
	if err != nil {
		return err
	}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--addr":
			if i+1 >= len(args) {
				return fmt.Errorf("magpie friday-bridge: --addr needs host:port")
			}
			cfg.Addr = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--addr="):
			cfg.Addr = strings.TrimPrefix(args[i], "--addr=")
		default:
			return fmt.Errorf("magpie friday-bridge: unknown %q", args[i])
		}
	}
	ln, err := fridaybridge.Listen(cfg)
	if err != nil {
		return err
	}
	fmt.Println(green.Render("●"), "Friday bridge on", bold.Render(cfg.Addr), muted.Render("→ "+cfg.RemoteHost+":"+cfg.RemotePort))
	return fridaybridge.Serve(context.Background(), ln, cfg)
}
