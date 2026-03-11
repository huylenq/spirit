package spirit

import "strings"

// Animals is the ordered list of 23 animal names (matches avatarAnimals in ui/avatar.go).
var Animals = [23]string{
	"Cat", "Dog", "Fish", "Frog", "Horse", "Crow", "Dove", "Dragon",
	"Hippo", "Otter", "Spider", "Kiwi", "Snake", "Bat", "Bee", "Bird",
	"Duck", "Owl", "Penguin", "Pig", "Rabbit", "Shark", "Cow",
}

// Adjectives maps [colorIdx][animalIdx] to a unique adjective.
// Each of the 184 adjectives is unique across the entire table.
var Adjectives = [8][23]string{
	// Fire (rose)
	{"Ember", "Molten", "Scorch", "Sear", "Kindle", "Char", "Smolder", "Inferno", "Flicker", "Singe", "Cinder", "Torch", "Ignite", "Pyre", "Spark", "Flare", "Fuse", "Brand", "Furnace", "Sizzle", "Burnt", "Blister", "Glow"},
	// Blaze (orange)
	{"Amber", "Russet", "Copper", "Tawny", "Sienna", "Auburn", "Sunset", "Bronze", "Ochre", "Ginger", "Maple", "Brass", "Rusty", "Topaz", "Cayenne", "Nutmeg", "Hazel", "Peach", "Spice", "Sorrel", "Coral", "Henna", "Buff"},
	// Solar (yellow)
	{"Golden", "Radiant", "Gleam", "Sunny", "Gilt", "Luster", "Dawn", "Glint", "Honey", "Saffron", "Lumen", "Lucent", "Flash", "Dazzle", "Ray", "Shimmer", "Noon", "Prism", "Beam", "Canary", "Aura", "Glare", "Maize"},
	// Acid (lime)
	{"Toxic", "Venom", "Neon", "Blight", "Caustic", "Plague", "Hazard", "Wicked", "Sting", "Sap", "Fungal", "Lichen", "Feral", "Viper", "Swamp", "Thorn", "Moss", "Pest", "Lurk", "Brute", "Crude", "Slime", "Murk"},
	// Frost (cyan)
	{"Frozen", "Glacial", "Arctic", "Frigid", "Polar", "Boreal", "Rime", "Crystal", "Alpine", "Sleet", "Hail", "Tundra", "Gelid", "Brisk", "Shiver", "Bitter", "Floe", "Drift", "Pale", "Chilled", "Flurry", "Icicle", "Crisp"},
	// Storm (indigo)
	{"Thunder", "Arcane", "Mystic", "Tempest", "Vortex", "Phantom", "Wraith", "Astral", "Eclipse", "Shade", "Void", "Nebula", "Twilight", "Specter", "Shadow", "Gale", "Bolt", "Lunar", "Abyss", "Cobalt", "Dusk", "Surge", "Rune"},
	// Bloom (pink)
	{"Petal", "Blush", "Flora", "Nectar", "Velvet", "Orchid", "Silk", "Peony", "Dainty", "Satin", "Dahlia", "Lotus", "Plush", "Tulip", "Cherub", "Fairy", "Pastel", "Posy", "Lace", "Blossom", "Suede", "Rouge", "Grace"},
	// Reef (teal)
	{"Tide", "Lagoon", "Kelp", "Surf", "Marina", "Tropic", "Shoal", "Atoll", "Brine", "Pearl", "Coastal", "Vapor", "Marsh", "Dew", "Spring", "Ripple", "Cove", "Isle", "Current", "Foam", "Brook", "Depth", "Eddy"},
}

// MnemonicName returns the human-readable mnemonic, e.g. "Ember Cat".
func MnemonicName(colorIdx, animalIdx int) string {
	ci := colorIdx % len(Adjectives)
	ai := animalIdx % len(Adjectives[0])
	return Adjectives[ci][ai] + " " + Animals[ai]
}

// WorktreeName returns the kebab-case worktree name, e.g. "ember-cat".
func WorktreeName(colorIdx, animalIdx int) string {
	return strings.ToLower(strings.ReplaceAll(MnemonicName(colorIdx, animalIdx), " ", "-"))
}
