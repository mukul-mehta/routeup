package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/mukul-mehta/routeup/internal/ipc"
	"github.com/mukul-mehta/routeup/internal/route"
)

func dashboardWindow(total, cursor, size int) (int, int) {
	size = max(1, size)
	start := max(0, cursor-size/2)
	start = min(start, max(0, total-size))
	return start, min(total, start+size)
}

func formatDashboardRoute(claim ipc.Claim, width int, styles terminalStyles, tlsPort int, header bool) string {
	name := terminalEscapeString(claim.Name)
	local := "-"
	targets := terminalEscapeString(formatTargets(claim.Targets))
	age := humanDuration(time.Since(claim.RegisteredAt))
	if header {
		name, local, targets, age = "NAME", "LOCAL URL", "TARGETS", "AGE"
	} else if routeName, err := route.Parse(claim.Name); err == nil {
		local = localURL(routeName.LocalHost(), tlsPort)
	}

	if width < 70 {
		name = fitTerminalText(name, 18)
		local = fitTerminalText(local, max(4, width-20))
		if header {
			return styles.label(name) + "  " + styles.label(local)
		}
		return styles.accent(name) + "  " + styles.url(local)
	}
	name = fitTerminalText(name, 18)
	age = fitTerminalText(age, 6)
	if width < 100 {
		local = fitTerminalText(local, max(4, width-30))
		if header {
			return strings.Join([]string{styles.label(name), styles.label(local), styles.label(age)}, "  ")
		}
		return strings.Join([]string{styles.accent(name), styles.url(local), styles.muted(age)}, "  ")
	}
	local = fitTerminalText(local, 34)
	targets = fitTerminalText(targets, max(8, width-64))
	if header {
		return strings.Join([]string{styles.label(name), styles.label(local), styles.label(targets), styles.label(age)}, "  ")
	}
	return strings.Join([]string{styles.accent(name), styles.url(local), targets, styles.muted(age)}, "  ")
}

func formatDashboardExposure(exposure ipc.ExposureStatus, width int, styles terminalStyles, header bool) string {
	state := terminalEscapeString(string(exposure.State))
	routeName := terminalEscapeString(exposure.Route)
	publicURL := "https://" + terminalEscapeString(exposure.Host)
	paths := terminalEscapeString(formatExposePaths(exposure.Paths))
	if header {
		state, routeName, publicURL, paths = "STATE", "ROUTE", "PUBLIC URL", "PATHS"
	}
	state = fitTerminalText(state, 12)
	if width < 70 {
		publicURL = fitTerminalText(publicURL, max(4, width-14))
		if header {
			return styles.label(state) + "  " + styles.label(publicURL)
		}
		return dashboardExposureState(styles, exposure.State, state) + "  " + styles.url(publicURL)
	}
	routeName = fitTerminalText(routeName, 18)
	if width < 100 {
		publicURL = fitTerminalText(publicURL, max(4, width-34))
		if header {
			return strings.Join([]string{styles.label(state), styles.label(routeName), styles.label(publicURL)}, "  ")
		}
		return strings.Join([]string{dashboardExposureState(styles, exposure.State, state), styles.accent(routeName), styles.url(publicURL)}, "  ")
	}
	paths = fitTerminalText(paths, 18)
	publicURL = fitTerminalText(publicURL, max(8, width-54))
	if header {
		return strings.Join([]string{styles.label(state), styles.label(routeName), styles.label(publicURL), styles.label(paths)}, "  ")
	}
	return strings.Join([]string{dashboardExposureState(styles, exposure.State, state), styles.accent(routeName), styles.url(publicURL), paths}, "  ")
}

func dashboardExposureState(styles terminalStyles, state ipc.ExposureState, value string) string {
	if state == ipc.ExposureReconnecting {
		return styles.warning(value)
	}
	return styles.success(value)
}

func pluralCount(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}
