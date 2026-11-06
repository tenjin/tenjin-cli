package project

import (
	"regexp"
	"strings"
)

var (
	// godotPresetSection is an export preset's header, [preset.0], and
	// godotOptionsSection the header of its options, [preset.0.options].
	godotPresetSection  = regexp.MustCompile(`^\[preset\.(\d+)\]$`)
	godotOptionsSection = regexp.MustCompile(`^\[preset\.(\d+)\.options\]$`)
	godotPluginVersion  = regexp.MustCompile(`(?m)^version="([^"]*)"`)
)

// GodotAddon is where the Tenjin addon keeps its plugin file, which
// project.godot names to enable it.
const GodotAddon = "addons/tenjin/plugin.cfg"

// GodotPreset is one export preset in export_presets.cfg: a platform, and
// the options set for it, with their values unquoted.
type GodotPreset struct {
	Name     string
	Platform string
	Options  map[string]string
	// Line is where the preset starts, and OptionLines where each option
	// is set.
	Line        int
	OptionLines map[string]int
}

// GodotPresets reads the export presets of the Godot project at root, in
// the order the file has them. Nil when there are none.
func GodotPresets(root string) []GodotPreset {
	d := &detector{root: root, p: &Project{}}
	return d.godotPresets()
}

func (d *detector) godotPresets() []GodotPreset {
	var presets []GodotPreset
	index := map[string]int{}
	at := func(n string) *GodotPreset {
		i, ok := index[n]
		if !ok {
			i = len(presets)
			index[n] = i
			presets = append(presets, GodotPreset{Options: map[string]string{}, OptionLines: map[string]int{}})
		}
		return &presets[i]
	}
	var current *GodotPreset
	options := false
	for n, line := range strings.Split(d.read("export_presets.cfg"), "\n") {
		line = strings.TrimSpace(line)
		if m := godotPresetSection.FindStringSubmatch(line); m != nil {
			current, options = at(m[1]), false
			current.Line = n + 1
			continue
		}
		if m := godotOptionsSection.FindStringSubmatch(line); m != nil {
			current, options = at(m[1]), true
			continue
		}
		if strings.HasPrefix(line, "[") {
			current = nil
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if current == nil || !ok {
			continue
		}
		value = godotValue(value)
		switch {
		case options:
			current.Options[key] = value
			current.OptionLines[key] = n + 1
		case key == "name":
			current.Name = value
		case key == "platform":
			current.Platform = value
		}
	}
	return presets
}

// godotValue is a value from a Godot config file: a quoted string
// unquoted, anything else (true, a number) as written.
func godotValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return godotUnquote(v[1 : len(v)-1])
	}
	return v
}

// godotUnquote undoes the escapes Godot writes in a quoted string.
func godotUnquote(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(s)
}

// godot matches a Godot project: project.godot at the root. The bundle IDs
// are in the export presets, one per platform; the Tenjin addon is copied
// into addons/tenjin.
func (d *detector) godot() bool {
	if !d.exists("project.godot") {
		return false
	}
	d.p.Framework = Godot
	d.p.Supported = true

	platforms := map[string]Platform{"Android": Android, "iOS": IOS}
	options := map[Platform]string{Android: "package/unique_name", IOS: "application/bundle_identifier"}
	for _, preset := range d.godotPresets() {
		platform, ok := platforms[preset.Platform]
		if !ok {
			continue
		}
		id := strings.TrimSpace(preset.Options[options[platform]])
		if id == "" {
			continue
		}
		if t := d.p.Target(platform); t != nil {
			if id != t.BundleID && !contains(t.OtherBundleIDs, id) {
				t.OtherBundleIDs = append(t.OtherBundleIDs, id)
			}
			continue
		}
		d.p.Targets = append(d.p.Targets, Target{Platform: platform, BundleID: id, Source: "export_presets.cfg"})
	}
	if t := d.p.Target(Android); t != nil && strings.Contains(t.BundleID, "$genname") {
		d.note("The Android export preset's package name is still Godot's default, %s. Set Package > Unique Name in the Android export preset to the app's package name.", t.BundleID)
	}
	if len(d.p.Targets) == 0 {
		d.note("There are no Android or iOS export presets in export_presets.cfg. Add them in Project > Export, so the bundle IDs can be read.")
	}

	if d.exists(GodotAddon) {
		i := Integration{Package: "tenjin-godot-sdk", Source: GodotAddon}
		if m := godotPluginVersion.FindStringSubmatch(d.read(GodotAddon)); m != nil {
			i.Version = m[1]
		}
		d.found(i)
	}
	return true
}

// BlankHashComments blanks GDScript's # comments, keeping line numbers and
// offsets, and leaves strings as they are: "#ffffff" is no comment.
func BlankHashComments(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); {
		switch b[i] {
		case '"', '\'':
			i = endOfString(s, i)
		case '#':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		default:
			i++
		}
	}
	return string(b)
}
