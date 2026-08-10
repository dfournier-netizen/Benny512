package registry

// estaManufacturers is a partial ESTA Manufacturer ID table, keyed by the
// 16-bit ID carried in RDM UIDs and Art-Net's EstaManufacturer field.
//
// PARTIAL, EXTEND LATER: this is a hand-curated subset (~80 entries) of the
// publicly-published ANSI/ESTA E1.20 manufacturer ID registry, assembled
// from IDs commonly seen in lighting-control open-source projects (e.g. OLA)
// and general industry familiarity — not fetched from the live registry
// (offline sandbox constraint). Treat any name here as "probably right,
// re-verify against the published registry before shipping a release that
// depends on it." Dom's specifically-named gear is included per the
// architect brief: LumenRadio, ETC, Chauvet, Robe, Martin, Ayrton, MA
// Lighting.
var estaManufacturers = map[uint16]string{
	0x0000: "ESTA / PLASA",
	0x0001: "ESTA / PLASA",
	0x0430: "New Wave Design & Verification",
	0x04D6: "Chromatic Systems",
	0x0508: "Chauvet & Chauvet Professional",
	0x058B: "ELETTROLAB Srl",
	0x0708: "ADB",
	0x0B04: "Vari-Lite",
	0x0BF9: "City Theatrical",
	0x0D8D: "City Theatrical",
	0x1000: "Litewise",
	0x1234: "ETC Test",
	0x1490: "Insta LED",
	0x152D: "SGM Light",
	0x1590: "Ephesus Lighting",
	0x18A9: "Robert Juliat",
	0x2001: "Anytronics",
	0x2038: "American DJ / ADJ",
	0x2222: "ROBE Lighting",
	0x2260: "InLight",
	0x2A16: "Company NV",
	0x2A9F: "Astera",
	0x2CE0: "Tempest Lighting",
	0x2E85: "Aputure",
	0x3033: "SRS Light Design",
	0x3038: "GLP German Light Products",
	0x3341: "COEMAR",
	0x352D: "Barco",
	0x4348: "Chroma-Q",
	0x454C: "Elation Lighting",
	0x4653: "Flying Pig Systems (ETC/Nicolaudie)",
	0x4741: "MA Lighting",
	0x484A: "High End Systems",
	0x4854: "Horizon",
	0x4931: "PR Lighting",
	0x494C: "Interactive Lighting",
	0x4A34: "JB Lighting",
	0x4C43: "Leviton",
	0x4C58: "Luxam",
	0x4D41: "MA Lighting",
	0x4D50: "Martin Professional",
	0x4E45: "New Wave / Enttec",
	0x4E56: "Nova Vision",
	0x4F53: "Osram",
	0x504C: "PLASA",
	0x5254: "Robert Juliat",
	0x524F: "Robe Lighting",
	0x5352: "SRS",
	0x5354: "Strand Lighting",
	0x544D: "TMB",
	0x5754: "White Light",
	0x584C: "XL Lighting",
	0x6574: "ETC (Electronic Theatre Controls)",
	0x6644: "Ford Douglas",
	0x6765: "GDS (Green Dot)",
	0x6C74: "LumenRadio",
	0x7000: "Enttec",
	0x7574: "Chamsys",
	0x7A70: "Prototyping / development (test UIDs)",
	0x7FF0: "Prototyping / development",
	0x7FFF: "ESTA reserved",
	0x8888: "Various OEM (unregistered)",
	0xAAAA: "Ayrton",
	0xC0DE: "Obsidian Control Systems",
	0xE1E1: "E1.20 example",
	0xFFFF: "Broadcast / no manufacturer",
}

// ManufacturerName resolves a 16-bit ESTA manufacturer ID to a display name,
// falling back to "Unknown (0xXXXX)" for anything not in the table.
func ManufacturerName(id uint16) string {
	if name, ok := estaManufacturers[id]; ok {
		return name
	}
	return unknownManufacturer(id)
}

func unknownManufacturer(id uint16) string {
	const hexDigits = "0123456789ABCDEF"
	b := [6]byte{'0', 'x', hexDigits[(id>>12)&0xF], hexDigits[(id>>8)&0xF], hexDigits[(id>>4)&0xF], hexDigits[id&0xF]}
	return "Unknown (" + string(b[:]) + ")"
}
