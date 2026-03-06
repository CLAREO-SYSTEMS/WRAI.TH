// 32x32 pixel-art sci-fi character sprites — Retro-Futurism / Cyberpunk style
// 6 archetypes — assigned by agent name hash
// Designed for maximum silhouette distinction at 64px rendered size
//
// Letters map to palette colors:
//   . = transparent, D = dark outline, H = head/helmet, h = head highlight
//   S = skin, E = eyes, M = mouth, C = coat/suit, c = coat detail
//   A = accent, L = legs, B = boots, V = visor, G = glow/LED, g = glow dim

const ARCHETYPE_NAMES = ["astronaut", "hacker", "droid", "cyborg", "captain", "wraith"];

// Each archetype: base frame + alternate pose. 4-frame cycle generated:
//   frame 0 = base, frame 1 = base bob, frame 2 = alt pose, frame 3 = alt bob
// Key design principle: each silhouette is RADICALLY different shape at small size
const ARCHETYPE_FRAMES = {
  // ASTRONAUT — bulky round helmet, wide suit, heavy boots
  astronaut: [
    "................................",
    "................................",
    "..........DDDDDDDDD............",
    ".........DhhhhhhhhhD...........",
    "........DHHHHHHHHHHhD..........",
    "........DHHHHHHHHHHhD..........",
    "........DHVVVVVVVVHHD..........",
    "........DHVVSEESSVVHD..........",
    "........DHVVSSMSSV.HD..........",
    "........DHHHHHHHHHHhD..........",
    ".........DhhhhhhhhhD...........",
    "..........DDDDDDDDD...........",
    "...........DCCCCCD.............",
    "..........DCcCCCcCD............",
    ".........DCcCGGCcCCD...........",
    "........ADCCCGGCCCCDa..........",
    "........ADCCCCCCCCCDA..........",
    "........ADCCCCCCCCCDA..........",
    ".........DCCCCCCCCCD...........",
    "..........DCCCCCCD.............",
    "..........DDLLDLLDD............",
    ".........DLL.DD.LLD...........",
    ".........DLL.DD.LLD...........",
    ".........DLL....LLD...........",
    "........DBBBD..DBBBD..........",
    "........DBBBD..DBBBD..........",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
  ],
  // HACKER — hood/asymmetric, slim build, sneaky posture
  hacker: [
    "................................",
    "................................",
    "...........DDDDDDD.............",
    "..........DHHHHHHDD............",
    ".........DHHHHHHHHDD...........",
    "........DHHHHHHHHHHHD..........",
    "........DHHHHHHHHHHDD..........",
    ".........DHSSSSSHD.............",
    ".........DSEEEESSD.............",
    ".........DSSMMSSD..............",
    "..........DSSSSD...............",
    "...........DHHD................",
    "..........DCCCCD...............",
    ".........DCCCCCCD..............",
    ".........DCcCCcCCD.............",
    "........GDCcCCcCCDG............",
    ".........DCCCCCCCD.............",
    ".........DCCCCCCCD.............",
    "..........DCCCCD...............",
    "..........DCCCCD...............",
    "..........DLLDLLD..............",
    "..........DLL.LLD..............",
    ".........DLL..LLD.............",
    ".........DLL..LLD.............",
    ".........DBB..BBD..............",
    ".........DBB..BBD..............",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
  ],
  // DROID — boxy head, antenna, mechanical legs, wide torso
  droid: [
    "................................",
    "...........DGD..................",
    "...........DGD..................",
    "...........DAD..................",
    ".........DDDDDDDDD............",
    "........DVVVVVVVVVVD...........",
    "........DVVEEVVVEEVD...........",
    "........DVVVVVVVVVVD...........",
    "........DVVVGGGVVVVD...........",
    "........DVVVVVVVVVVD...........",
    ".........DDDDDDDDD............",
    "..........DDAADD...............",
    ".........DCCCCCCCD.............",
    "........DCCCCCCCCCD............",
    "........DCCGGGGGGCCD...........",
    "........DCCGGGGGGCCD...........",
    "........DACCCCCCCCAD...........",
    "........DACCCCCCCCAD...........",
    ".........DCCCCCCCD.............",
    ".........DDDDDDDDDD...........",
    ".........DA.D..D.AD...........",
    "........DLLD.DD.DLLD..........",
    "........DLDD.DD.DDLD..........",
    "........DLLD.DD.DLLD..........",
    "........DLDD....DDLD..........",
    "........DBBD....DBBD..........",
    ".........DDD....DDD...........",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
  ],
  // CYBORG — half-face visor, asymmetric body, glowing arm
  cyborg: [
    "................................",
    "................................",
    "................................",
    "..........DDDDDDDDD...........",
    ".........DHHHHHHVVVD...........",
    "........DHHHHHVVVVVD...........",
    "........DHHHSSVVGGVD...........",
    "........DHHSEESEGVVD...........",
    "........DHHSSMSSVVVD...........",
    "........DHHHSSSHHHHD...........",
    ".........DDDHHHDDDD............",
    ".........ADCCCCCDA.............",
    "........ADDCCCCCCDA............",
    "........ADCCCCCCCCDGA..........",
    "........ADCCGCCCCCGDA..........",
    "........ADCCCCCCCCCDGA.........",
    ".........ADCCCCCCCDA...........",
    ".........DCCCCCCCCCD...........",
    "..........DCCCCCCD.............",
    "..........DCCCCCCD.............",
    "..........DLLDLLDA.............",
    ".........Gdll.LLDGA...........",
    ".........DLL..LLD.............",
    ".........DLL..LLD.............",
    ".........DBBG.GBBDA...........",
    ".........DBBD..BBDA...........",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
  ],
  // CAPTAIN — wide hat/brim, broad shoulders, medals, authoritative
  captain: [
    "................................",
    "................................",
    ".........DDDDDDDDDDD..........",
    "........DAAAAAAAAAAAAD.........",
    "........DAAAAAAAAAAAAD.........",
    ".......DDDDDDDDDDDDDDD.......",
    ".........DHHHHHHHHHD...........",
    "........DHHSSSSSSHHHD..........",
    "........DHHSEEESSHHHD..........",
    "........DHSSSSMSSSSHD..........",
    ".........DDSSSSSDD.............",
    ".........DDDHHHDDDD............",
    "........AADCCCCCCCAA...........",
    ".......AADCCCCCCCCDDAA.........",
    ".......ADCCCCCCCCCCCDA.........",
    ".......ADCCCCCCCCCCDA..........",
    ".......ADCCCCCCCCCCDA..........",
    "........DGGGGGGGGGGGD..........",
    "........DCCCCCCCCCCCD..........",
    ".........DCCCCCCCCD............",
    "..........DCCCCCD..............",
    "..........DLLDLLD..............",
    ".........DLL..LLD.............",
    ".........DLL..LLD.............",
    ".........DLL..LLD.............",
    "........DBBBB.BBBBD...........",
    "........DBBBB.BBBBD...........",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
  ],
  // WRAITH — tall, flowing cloak, no visible legs, ghostly
  wraith: [
    "................................",
    "................................",
    "............DDD.................",
    "...........DHHDD...............",
    "..........DHHHHDD..............",
    ".........DHHHHHHHD.............",
    "........DHHHHHHHHDD............",
    "........DHHDDDDDHHD...........",
    "........DHD.GG.DDHD...........",
    "........DHDD..DDHHD...........",
    ".........DHHHHHHHD.............",
    ".........DCCCCCCCD.............",
    "........DCCCCCCCCCD............",
    ".......DCCCCCCCCCCCCD..........",
    "......DCCCCCCCCCCCCCCCD........",
    "......DCCCCCCCCCCCCCCCCD.......",
    ".....DCCCCCCCCCCCCCCCCCCD......",
    ".....DCCCCCCCCCCCCCCCCCCCD.....",
    "......DCCCCCCCCCCCCCCCCCCD.....",
    "......DCCCCCCCCCCCCCCCCCD......",
    ".......DCCCCCCCCCCCCCCCCD......",
    "........DCCCCCCCCCCCCCD........",
    ".........DCCCCCCCCCCD..........",
    "..........DcCCCCcCD............",
    "...........Dc..Dc.D...........",
    "...........D....D..............",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
  ],
};

// Alternate pose: per-archetype row overrides (only changed rows)
// Creates visible arm swings, leg steps, and character-specific motion
const ARCHETYPE_ALT_ROWS = {
  // ASTRONAUT — arms raised wider, legs in walking step
  astronaut: {
    15: "......AADCCCGGCCCCDDAA.........",
    16: "......AADDCCCCCCCCDAA..........",
    17: ".........DCCCCCCCCCCD..........",
    21: "........DLL..DD..LLD...........",
    22: "........DLL..DD...LLD..........",
    23: "........DLL.......LLD..........",
    24: ".......DBBBD...DBBBD...........",
    25: ".......DBBBD...DBBBD...........",
  },
  // HACKER — hunched forward typing, wider stance
  hacker: {
    13: "........DCCCCCCCD..............",
    14: "........DCcCCcCCCD.............",
    15: ".......GDCcCCcCCCDG............",
    16: "........DCCCCCCCCD.............",
    17: ".........DCCCCCCCD.............",
    21: "........DLL...LLD.............",
    22: "........DLL...LLD.............",
    24: "........DBB...BBD..............",
    25: "........DBB...BBD..............",
  },
  // DROID — antenna tilted, LED dim, right leg forward
  droid: {
    1:  "............DGD.................",
    2:  ".............DGD................",
    3:  ".............DAD................",
    8:  "........DVVVgggVVVVD...........",
    21: "........DLLD..DD.DLLD.........",
    22: "........DLDD..DD.DDLD.........",
    24: "........DLLD.....DLLD.........",
    25: "........DBBD.....DBBD.........",
  },
  // CYBORG — glow arm extended further, wider stance
  cyborg: {
    12: "........ADDCCCCCCDGA...........",
    13: "........ADCCCCCCCCGDA..........",
    14: ".......ADCCGCCCCCCGDA.........",
    15: ".......ADCCCCCCCCCCDGA........",
    21: "........GDLL..LLDGA...........",
    22: "........DLL...LLD.............",
    24: "........DBBG..GBBDA...........",
    25: "........DBBD...BBDA...........",
  },
  // CAPTAIN — right arm raised commanding, wider stance
  captain: {
    13: "......AADCCCCCCCCDDAAA.........",
    14: ".......ADCCCCCCCCCCCDA.........",
    15: ".......ADCCCCCCCCCCDDA.........",
    16: ".......ADCCCCCCCCCCDA.........",
    22: "........DLL...LLD.............",
    23: "........DLL...LLD.............",
    24: "........DLL...LLD.............",
    25: ".......DBBBB..BBBBD...........",
    26: ".......DBBBB..BBBBD...........",
  },
  // WRAITH — cloak flowing opposite direction (billowing left)
  wraith: {
    14: ".....DCCCCCCCCCCCCCCCCD........",
    15: "....DCCCCCCCCCCCCCCCCCD........",
    16: "....DCCCCCCCCCCCCCCCCCCD.......",
    17: ".....DCCCCCCCCCCCCCCCCCD.......",
    18: ".....DCCCCCCCCCCCCCCCCCCD.....",
    19: "......DCCCCCCCCCCCCCCCCCD.....",
    20: ".......DCCCCCCCCCCCCCCD.......",
    21: "........DCCCCCCCCCCCCCD.......",
    22: ".........DCCCCCCCCCCD.........",
    23: "..........DcCCCCcCD...........",
    24: "..........Dc...Dc.D...........",
    25: "..........D.....D..............",
  },
};

// Color palettes — Cyberpunk / Retro-Futurism style
// Each palette uses neon + dark contrast per design system recommendations
const PALETTES = [
  { // 0 — Neon Cyan (primary cyberpunk)
    H: "#00FFFF", h: "#7FFFFF", S: "#ffeaa7", E: "#ffffff", M: "#e17055",
    C: "#008B8B", c: "#00CED1", A: "#FF006E", L: "#1a1a2e", B: "#2d3436",
    D: "#0a0a18", V: "#00E5FF", G: "#00FF88", g: "#00cc6a",
  },
  { // 1 — Hot Pink / Magenta
    H: "#FF006E", h: "#FF69B4", S: "#ffeaa7", E: "#ffffff", M: "#e17055",
    C: "#C70050", c: "#FF1493", A: "#00FFFF", L: "#1a1a2e", B: "#2d3436",
    D: "#0a0a18", V: "#FF69B4", G: "#FF00FF", g: "#CC00CC",
  },
  { // 2 — Neon Blue
    H: "#0080FF", h: "#4DA6FF", S: "#ffeaa7", E: "#ffffff", M: "#e17055",
    C: "#0059B3", c: "#3399FF", A: "#FF006E", L: "#1a1a2e", B: "#2d3436",
    D: "#0a0a18", V: "#66B3FF", G: "#00D2FF", g: "#0097e6",
  },
  { // 3 — Matrix Green
    H: "#00FF00", h: "#66FF66", S: "#ffeaa7", E: "#ffffff", M: "#e17055",
    C: "#008000", c: "#00CC00", A: "#FF006E", L: "#1a1a2e", B: "#2d3436",
    D: "#0a0a18", V: "#33FF33", G: "#00FF88", g: "#00b36b",
  },
  { // 4 — Neon Purple (Vaporwave)
    H: "#B967FF", h: "#D5A0FF", S: "#ffeaa7", E: "#ffffff", M: "#e17055",
    C: "#7C3AED", c: "#A78BFA", A: "#FF006E", L: "#1a1a2e", B: "#2d3436",
    D: "#0a0a18", V: "#D5C8FF", G: "#E056FD", g: "#be2edd",
  },
  { // 5 — Ember / Synthwave Orange
    H: "#FF9F43", h: "#FFCA80", S: "#ffeaa7", E: "#ffffff", M: "#e17055",
    C: "#E17055", c: "#FF7675", A: "#00FFFF", L: "#1a1a2e", B: "#2d3436",
    D: "#0a0a18", V: "#FFBE76", G: "#FF6348", g: "#ee5a24",
  },
];

// GOLDEN palette — 0.1% chance (1 in 1000)
const GOLDEN_PALETTE = {
  H: "#ffd700", h: "#fff3a0", S: "#ffe4b5", E: "#ffffff", M: "#daa520",
  C: "#daa520", c: "#ffd700", A: "#fff3a0", L: "#5c4a00", B: "#8b6914",
  D: "#3a2f00", V: "#ffec8b", G: "#fffacd", g: "#ffe066",
};

// Palette accent color for name tags — neon primaries
export const PALETTE_COLORS = PALETTES.map(p => p.H);
// Golden gets a special gold entry
PALETTE_COLORS.push("#ffd700");

// Activity → ring color mapping
export const ACTIVITY_GLOW = {
  typing:   "#00e676",  // green
  reading:  "#00bcd4",  // cyan
  terminal: "#ff9800",  // orange
  browsing: "#aa00ff",  // violet
  thinking: "#ffeb3b",  // yellow
  waiting:  "#ff1744",  // red (user input needed)
  idle:     null,
};

// Activity → frame speed (seconds per frame)
export const ACTIVITY_FRAME_SPEED = {
  typing:   0.2,
  reading:  0.4,
  terminal: 0.3,
  browsing: 0.35,
  thinking: 0.8,
  waiting:  1.2,
  idle:     0.6,
};

const cache = new Map();

/** Deterministic hash from string -> integer */
function hashName(name) {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  }
  return Math.abs(hash);
}

/** Generate bob frame by shifting sprite down 1px */
function makeBobFrame(baseFrame) {
  const empty = ".".repeat(32);
  return [empty, ...baseFrame.slice(0, -1)];
}

/** Generate alternate pose by applying row overrides */
function makeAltFrame(baseFrame, archetype) {
  const mods = ARCHETYPE_ALT_ROWS[archetype];
  if (!mods) return baseFrame;
  return baseFrame.map((row, i) => mods[i] !== undefined ? mods[i] : row);
}

export class SpriteGenerator {
  /**
   * Generate sprite frames for an agent.
   * @param {number} paletteIndex - color palette
   * @param {string} agentName - used to pick archetype + golden lottery
   * @returns {{ frames: OffscreenCanvas[], archetype: string, isGolden: boolean, paletteIndex: number }}
   */
  static generate(paletteIndex = 0, agentName = "") {
    // Determine archetype from name hash
    const nameHash = hashName(agentName || `agent_${paletteIndex}`);
    const archIdx = nameHash % ARCHETYPE_NAMES.length;
    const archetype = ARCHETYPE_NAMES[archIdx];

    // Golden lottery: use a secondary hash seed for fairness
    // 0.1% = 1 in 1000
    const goldenSeed = hashName(agentName + "__golden__");
    const isGolden = (goldenSeed % 1000) === 0;

    const cacheKey = `${archetype}_${paletteIndex}_${isGolden}`;
    if (cache.has(cacheKey)) {
      return cache.get(cacheKey);
    }

    const palette = isGolden ? GOLDEN_PALETTE : PALETTES[paletteIndex % PALETTES.length];
    const baseFrame = ARCHETYPE_FRAMES[archetype];
    const altFrame = makeAltFrame(baseFrame, archetype);

    // 4-frame cycle: base → base bob → alt pose → alt bob
    const rendered = {
      frames: [
        renderFrame(baseFrame, palette),
        renderFrame(makeBobFrame(baseFrame), palette),
        renderFrame(altFrame, palette),
        renderFrame(makeBobFrame(altFrame), palette),
      ],
      archetype,
      isGolden,
      paletteIndex: isGolden ? PALETTES.length : paletteIndex,
    };

    cache.set(cacheKey, rendered);
    return rendered;
  }
}

function renderFrame(frame, palette) {
  const size = 64; // 32 * 2
  const canvas = new OffscreenCanvas(size, size);
  const ctx = canvas.getContext("2d");
  const px = 2;

  for (let y = 0; y < 32; y++) {
    const row = frame[y];
    if (!row) continue;
    for (let x = 0; x < 32; x++) {
      const ch = row[x];
      if (ch === "." || ch === undefined) continue;
      const color = palette[ch];
      if (!color) continue;
      ctx.fillStyle = color;
      ctx.fillRect(x * px, y * px, px, px);
    }
  }

  return canvas;
}
