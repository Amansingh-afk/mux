package session

import (
	"math/rand/v2"
)

// codenames: single aesthetic words, 3–10 chars. Mix of celestial,
// mythological, natural, mineral, abstract, plus multilingual flavor.
var codenames = []string{
	// celestial
	"nova", "pulsar", "vega", "lyra", "orion", "rigel", "sirius", "altair",
	"corvus", "polaris", "andromeda", "halo", "comet", "astra", "nebula",
	// greek / norse mythology
	"atlas", "hydra", "phoenix", "sphinx", "kraken", "cyclops", "loki",
	"odin", "thor", "gaia", "hermes", "apollo", "styx", "nyx",
	"zeus", "hera", "ares", "hades", "nike", "iris", "eos", "helios",
	"selene", "titan", "kronos", "persephone", "aphrodite", "artemis",
	"dionysus", "poseidon", "athena",
	// hindi / indian mythology & cartoons
	"shiva", "krishna", "arjun", "hanuman", "ganesh", "kartik", "durga",
	"indra", "agni", "surya", "chandra", "varuna", "rudra", "vayu", "yama",
	"veer", "tara", "meena", "kiran", "rakshak", "rustom",
	"bheem", "motu", "patlu", "mogli", "raju", "kalia", "jaggu",
	"dholu", "bholu", "chacha", "nikku", "shaktiman", "chhota",
	// french
	"soleil", "lumiere", "reve", "brume", "fleur", "foret", "mer",
	"etoile", "vent", "pluie", "cristal", "noir", "blanc", "aurore",
	// spanish
	"sol", "luna", "mar", "cielo", "rio", "fuego", "rayo", "noche",
	"sombra", "tigre", "aguila", "lobo", "perla", "plata", "oro",
	"viento", "nube", "tormenta", "leon", "halcon",
	// nature
	"ember", "frost", "tide", "moss", "willow", "birch", "cedar", "cove",
	"cinder", "glacier", "storm", "flare", "bloom", "fern", "reef", "drift",
	"rift", "mesa", "fjord", "canyon", "meadow", "verdant",
	// minerals
	"onyx", "jade", "amber", "quartz", "opal", "topaz", "slate", "flint",
	"pyrite", "basalt", "obsidian", "marble",
	// abstract
	"cipher", "vector", "axiom", "quanta", "prism", "nexus", "vortex",
	"shard", "echo", "mirage", "zenith", "aurora", "helix", "tempest",
	"equinox", "solstice", "paragon", "umbra", "penumbra",
	// creatures
	"hawk", "falcon", "lynx", "raven", "wolf", "fox", "otter", "stoat",
	"panther", "heron", "kestrel", "osprey", "ibis",
	// colors / light
	"saffron", "indigo", "crimson", "azure", "ochre", "vermilion",
	"chartreuse", "ivory",
}

// NewCodename returns a random word not present in `taken`. If all words
// are taken, appends a suffix.
func NewCodename(taken map[string]bool) string {
	// shuffle-pick: avoid repeated collisions
	order := rand.Perm(len(codenames))
	for _, i := range order {
		w := codenames[i]
		if !taken[w] {
			return w
		}
	}
	// exhausted — suffix a digit
	for n := 2; n < 1000; n++ {
		base := codenames[rand.IntN(len(codenames))]
		w := base + "-" + itoa(n)
		if !taken[w] {
			return w
		}
	}
	return "agent"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [8]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
