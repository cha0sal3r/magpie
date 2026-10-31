// Package all registers every built-in plugin. Only magpie-friday's main imports
// it: core never imports a plugin, and a plugin never imports another.
package all

import (
	_ "github.com/cha0sal3r/magpie-friday/plugins/budget"
	_ "github.com/cha0sal3r/magpie-friday/plugins/gateway"
	_ "github.com/cha0sal3r/magpie-friday/plugins/groups"
	_ "github.com/cha0sal3r/magpie-friday/plugins/library"
	_ "github.com/cha0sal3r/magpie-friday/plugins/middleware"
	_ "github.com/cha0sal3r/magpie-friday/plugins/otel"
	_ "github.com/cha0sal3r/magpie-friday/plugins/redact"
	_ "github.com/cha0sal3r/magpie-friday/plugins/rotation"
	_ "github.com/cha0sal3r/magpie-friday/plugins/routing"
	_ "github.com/cha0sal3r/magpie-friday/plugins/search"
	_ "github.com/cha0sal3r/magpie-friday/plugins/sessions"
	_ "github.com/cha0sal3r/magpie-friday/plugins/tools"
	_ "github.com/cha0sal3r/magpie-friday/plugins/usage"
	_ "github.com/cha0sal3r/magpie-friday/plugins/warmups"
)
