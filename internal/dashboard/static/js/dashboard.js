// Mission Control — Fleet + Machine Map + Terminal Viewer

const POLL_INTERVAL = 3000;

// Markdown setup
const md = window.marked;
md.setOptions({
  highlight: (code, lang) => {
    if (window.hljs && lang && hljs.getLanguage(lang)) {
      return hljs.highlight(code, { language: lang }).value;
    }
    return code;
  },
  breaks: true,
});

// --- State ---
let fleetData = null;
let connected = false;
let terminalAgent = null;
let terminalLines = [];
let terminalPollTimer = null;
let animFrame = 0;

// --- DOM ---
const fleetGrid = document.getElementById("fleet-grid");
const agentCount = document.getElementById("agent-count");
const machineCount = document.getElementById("machine-count");
const connectionDot = document.getElementById("connection-dot");
const machineCanvas = document.getElementById("machine-canvas");
const ctx = machineCanvas.getContext("2d");

// Terminal
const terminalOverlay = document.getElementById("terminal-overlay");
const terminalClose = document.getElementById("terminal-close");
const terminalAgentName = document.getElementById("terminal-agent-name");
const terminalAgentState = document.getElementById("terminal-agent-state");
const terminalAgentMachine = document.getElementById("terminal-agent-machine");
const terminalTurnCount = document.getElementById("terminal-turn-count");
const terminalOutput = document.getElementById("terminal-output");
const terminalInput = document.getElementById("terminal-input");
const terminalSendBtn = document.getElementById("terminal-send");

// --- API ---

async function fetchFleet() {
  try {
    const res = await fetch("/api/fleet");
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    fleetData = data;
    setConnected(true);
    return data;
  } catch {
    setConnected(false);
    return null;
  }
}

async function fetchTerminal(agent) {
  try {
    const res = await fetch(`/api/terminal/${agent}`);
    if (!res.ok) return [];
    return await res.json();
  } catch { return []; }
}

async function sendToAgent(agent, content) {
  try {
    const res = await fetch(`/api/terminal/${agent}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content }),
    });
    return res.ok;
  } catch { return false; }
}

// --- Render ---

function setConnected(state) {
  connected = state;
  connectionDot.className = `connection-dot ${state ? "connected" : "disconnected"}`;
}

function formatTokens(n) {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function renderFleet(data) {
  if (!data || !data.agents) {
    fleetGrid.innerHTML = `<div class="empty-state" style="grid-column:1/-1"><div class="icon">&#9678;</div><p>No agents registered.</p></div>`;
    agentCount.textContent = "0";
    return;
  }

  const agents = data.agents;
  const names = Object.keys(agents).sort();
  agentCount.textContent = names.length;

  const machines = new Set(names.map(n => agents[n].machine || "local"));
  machineCount.textContent = machines.size;

  fleetGrid.innerHTML = names.map(name => {
    const a = agents[name];
    const state = a.state || "idle";
    const tokens = a.tokens_used || 0;
    const tokenLimit = a.token_limit || 0;
    const tokenPct = tokenLimit > 0 ? Math.min(100, (tokens / tokenLimit * 100)) : 0;
    const tokenColor = tokenPct > 80 ? "var(--red)" : tokenPct > 50 ? "var(--yellow)" : "var(--cyan)";

    return `
      <div class="agent-card" data-agent="${esc(name)}">
        <div class="agent-card-header">
          <span class="agent-name">${esc(name)}</span>
          <span class="agent-state state-${state}">${state}</span>
        </div>
        <div class="agent-stats">
          <div class="agent-stat">
            <span class="agent-stat-label">Turns</span>
            <span class="agent-stat-value">${a.turn_count || 0}</span>
          </div>
          <div class="agent-stat">
            <span class="agent-stat-label">Machine</span>
            <span class="agent-stat-value">${esc(a.machine || "local")}</span>
          </div>
        </div>
        ${tokens > 0 ? `
        <div class="agent-tokens">
          <div class="agent-tokens-header">
            <span class="agent-stat-label">Tokens</span>
            <span class="agent-tokens-count">${formatTokens(tokens)}${tokenLimit > 0 ? " / " + formatTokens(tokenLimit) : ""}</span>
          </div>
          <div class="agent-tokens-bar">
            <div class="agent-tokens-fill" style="width:${tokenPct || 2}%;background:${tokenColor}"></div>
          </div>
        </div>` : ""}
        <div class="agent-meta">
          <span class="agent-tag">${esc(a.machine || "local")}</span>
          ${a.session_id ? `<span class="agent-tag">session:${(a.session_id||"").slice(0,8)}</span>` : ""}
        </div>
      </div>`;
  }).join("");

  document.querySelectorAll(".agent-card").forEach(card => {
    card.addEventListener("click", () => openTerminal(card.dataset.agent));
  });
}

// ============================================================
// Pixel Art Machine Map — Mothership + Satellites
// ============================================================

const STATE_COLORS = {
  working: "#4ade80",
  idle: "#60a5fa",
  sleeping: "#555566",
  spawning: "#fbbf24",
  crashed: "#f87171",
  dead: "#f87171",
};

// Mothership — side-view capital ship, 40 cols x 15 rows
// Designed to read clearly at 5px scale (~200x75 px rendered)
// h=hull dark, H=hull mid, A=hull accent, w=window/light, e=engine glow,
// W=wing, T=thruster, d=detail, c=cockpit glass
const MOTHERSHIP = [
  "........................................",
  "................HHHHhh.................",
  "..............HHAAAAHHhh...............",
  "...........HHHAAAddAAHHHh.............",
  ".........HHHAAddddddAAHHHh...........",
  "..WWWWWHHHAAAdwwddwwdAAAHHHhWWWWW...",
  "..WddWWHHAAAAAAdddddAAAAHHHhWddW....",
  "..WWWWWHHHAAAdwwddwwdAAAHHHhWWWWW...",
  ".........HHHAAddddddAAHHHh...........",
  "...........HHHAAAddAAHHHh.............",
  "..............HHAAAAHHhh...............",
  "................HHHHhh.................",
  "..................TT...................",
  "..................ee...................",
  "..................ee...................",
];

// Satellite — compact 16x10 probe with solar panels
const SATELLITE = [
  "................",
  ".......HH.......",
  "......HAAH......",
  "..ppp.HAAH.ppp..",
  "..pcp.AAAA.pcp..",
  "..pcp.AAAA.pcp..",
  "..ppp.HAAH.ppp..",
  "......HAAH......",
  ".......hh.......",
  "................",
];

const PAL_STATION = {
  H: "#4a3d99", A: "#7c6aef", h: "#3b2d8a", d: "#5548b0",
  w: "#22d3ee", W: "#6b5ce7", e: "#a78bfa", T: "#4a3d99", c: "#4ade80",
};
const PAL_SAT = {
  H: "#1e40af", A: "#3b82f6", h: "#1e3a8a",
  p: "#1e40af", c: "#60a5fa",
};
const PAL_SAT_OFF = {
  H: "#2a2a3a", A: "#3a3a4a", h: "#222233",
  p: "#2a2a3a", c: "#444455",
};

function drawSprite(sprite, cx, cy, scale, palette) {
  const rows = sprite.length;
  const cols = sprite[0].length;
  const sx = cx - (cols * scale) / 2;
  const sy = cy - (rows * scale) / 2;
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      const ch = sprite[r][c];
      if (ch === ".") continue;
      ctx.fillStyle = palette[ch] || "#888";
      ctx.fillRect(Math.floor(sx + c * scale), Math.floor(sy + r * scale), scale, scale);
    }
  }
}

function drawGlow(cx, cy, radius, color, opacity) {
  const grad = ctx.createRadialGradient(cx, cy, 0, cx, cy, radius);
  const a = opacity || 0.12;
  grad.addColorStop(0, color + Math.round(a * 255).toString(16).padStart(2, "0"));
  grad.addColorStop(1, color + "00");
  ctx.fillStyle = grad;
  ctx.beginPath();
  ctx.arc(cx, cy, radius, 0, Math.PI * 2);
  ctx.fill();
}

function drawBeam(x1, y1, x2, y2, active) {
  ctx.save();
  if (active) {
    // Solid glowing beam
    ctx.strokeStyle = "rgba(124,106,239,0.25)";
    ctx.lineWidth = 3;
    ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2); ctx.stroke();
    ctx.strokeStyle = "rgba(124,106,239,0.5)";
    ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2); ctx.stroke();

    // Data packets
    for (let p = 0; p < 3; p++) {
      const t = ((animFrame * 0.6 + p * 33) % 100) / 100;
      const px = x1 + (x2 - x1) * t;
      const py = y1 + (y2 - y1) * t;
      ctx.fillStyle = "#a78bfa";
      ctx.shadowColor = "#a78bfa";
      ctx.shadowBlur = 8;
      ctx.beginPath();
      ctx.arc(px, py, 2.5, 0, Math.PI * 2);
      ctx.fill();
    }
  } else {
    ctx.strokeStyle = "rgba(255,255,255,0.04)";
    ctx.lineWidth = 1;
    ctx.setLineDash([3, 8]);
    ctx.beginPath(); ctx.moveTo(x1, y1); ctx.lineTo(x2, y2); ctx.stroke();
  }
  ctx.restore();
}

function drawStarfield(W, H) {
  ctx.fillStyle = "#0a0a12";
  ctx.fillRect(0, 0, W, H);

  // Stars — deterministic from seed
  for (let i = 0; i < 120; i++) {
    const sx = ((42 * (i + 1) * 7919) % 10000) / 10000 * W;
    const sy = ((42 * (i + 1) * 6271) % 10000) / 10000 * H;
    const brightness = 0.1 + ((i * 7) % 10) / 25;
    const twinkle = Math.sin(animFrame * 0.02 + i * 1.7) * 0.08;
    const size = i % 17 === 0 ? 2 : 1;
    ctx.fillStyle = `rgba(180, 190, 255, ${brightness + twinkle})`;
    ctx.fillRect(Math.floor(sx), Math.floor(sy), size, size);
  }

  // Nebula hints
  drawGlow(W * 0.15, H * 0.3, 80, "#7c6aef", 0.03);
  drawGlow(W * 0.85, H * 0.7, 60, "#3b82f6", 0.03);
}

function drawAgentDots(cx, y, agents) {
  if (!agents || agents.length === 0) return;
  const dotR = 5;
  const gap = 18;
  const startX = cx - ((agents.length - 1) * gap) / 2;

  agents.forEach((agent, i) => {
    const dx = startX + i * gap;
    const col = STATE_COLORS[agent.state] || STATE_COLORS.idle;

    if (agent.state === "working") {
      drawGlow(dx, y, 12, col, 0.3);
    }

    ctx.fillStyle = col;
    ctx.beginPath();
    ctx.arc(dx, y, dotR, 0, Math.PI * 2);
    ctx.fill();

    // Subtle ring
    ctx.strokeStyle = col + "44";
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.arc(dx, y, dotR + 3, 0, Math.PI * 2);
    ctx.stroke();
  });
}

function renderMachineMap(data) {
  const dpr = window.devicePixelRatio || 1;
  const rect = machineCanvas.getBoundingClientRect();
  machineCanvas.width = rect.width * dpr;
  machineCanvas.height = rect.height * dpr;
  ctx.scale(dpr, dpr);

  const W = rect.width;
  const H = rect.height;
  ctx.clearRect(0, 0, W, H);
  if (!data || !data.agents) return;

  // --- Group agents by machine ---
  const machines = {};
  for (const [name, agent] of Object.entries(data.agents)) {
    const m = agent.machine || "local";
    if (!machines[m]) machines[m] = { name: m, agents: [], isStation: false };
    machines[m].agents.push({ name, state: agent.state || "idle" });
  }

  const stationName = data.machine || "station";
  if (machines[stationName]) machines[stationName].isStation = true;
  else machines[stationName] = { name: stationName, agents: [], isStation: true };

  const machineList = Object.values(machines);
  machineList.sort((a, b) => a.isStation ? -1 : b.isStation ? 1 : a.name.localeCompare(b.name));

  const station = machineList[0];
  const satellites = machineList.slice(1);

  // --- Background ---
  drawStarfield(W, H);

  const shipCX = W * 0.32;
  const shipCY = H * 0.42;

  // --- Beams (draw first, behind sprites) ---
  if (satellites.length > 0) {
    const satZoneX = W * 0.72;
    const satSpacing = H / (satellites.length + 1);

    satellites.forEach((sat, i) => {
      const satY = satSpacing * (i + 1);
      const hasActive = sat.agents.some(ag => ag.state === "working" || ag.state === "idle");
      drawBeam(shipCX + 50, shipCY, satZoneX, satY, hasActive);
    });
  }

  // --- Mothership ---
  const shipScale = 5;
  drawGlow(shipCX, shipCY, 100, "#7c6aef", 0.08);
  drawSprite(MOTHERSHIP, shipCX, shipCY, shipScale, PAL_STATION);

  // Engine exhaust
  const exBaseY = shipCY + (MOTHERSHIP.length * shipScale) / 2;
  for (let i = 0; i < 6; i++) {
    const flicker = Math.sin(animFrame * 0.12 + i * 1.5) * 0.3 + 0.5;
    const ey = exBaseY + i * 4;
    const w = (6 - i) * 1.5;
    ctx.fillStyle = `rgba(167, 139, 250, ${flicker * 0.5})`;
    ctx.fillRect(shipCX - w, ey, w * 2, 3);
  }

  // Station label
  ctx.fillStyle = "#e0e0e8";
  ctx.font = "bold 12px 'JetBrains Mono'";
  ctx.textAlign = "center";
  ctx.fillText(station.name, shipCX, shipCY + 65);
  ctx.fillStyle = "#7c6aef";
  ctx.font = "10px 'JetBrains Mono'";
  ctx.fillText("STATION", shipCX, shipCY + 79);

  // Station agent dots
  drawAgentDots(shipCX, shipCY + 96, station.agents);

  // --- Satellites ---
  if (satellites.length > 0) {
    const satZoneX = W * 0.72;
    const satSpacing = H / (satellites.length + 1);
    const satScale = 4;

    satellites.forEach((sat, i) => {
      const satY = satSpacing * (i + 1);
      const hasActive = sat.agents.some(ag => ag.state === "working" || ag.state === "idle");

      if (hasActive) drawGlow(satZoneX, satY, 40, "#3b82f6", 0.08);

      const pal = hasActive ? PAL_SAT : PAL_SAT_OFF;
      drawSprite(SATELLITE, satZoneX, satY, satScale, pal);

      // Label
      ctx.fillStyle = "#c0c0d0";
      ctx.font = "bold 11px 'JetBrains Mono'";
      ctx.textAlign = "center";
      ctx.fillText(sat.name, satZoneX, satY + 32);
      ctx.fillStyle = "#3b82f6";
      ctx.font = "9px 'JetBrains Mono'";
      ctx.fillText("SATELLITE", satZoneX, satY + 44);

      // Agent dots
      drawAgentDots(satZoneX, satY + 58, sat.agents);
    });
  }
}

// ============================================================
// Terminal Panel
// ============================================================

function openTerminal(agentName) {
  terminalAgent = agentName;
  terminalLines = [];

  terminalAgentName.textContent = agentName;
  const info = fleetData?.agents?.[agentName];
  if (info) {
    const state = info.state || "idle";
    terminalAgentState.className = `agent-state state-${state}`;
    terminalAgentState.textContent = state;
    terminalAgentMachine.textContent = info.machine || "local";
    terminalTurnCount.textContent = `turn ${info.turn_count || 0}`;
  }

  terminalOverlay.classList.remove("hidden");
  terminalInput.focus();
  loadTerminal(agentName);

  if (terminalPollTimer) clearInterval(terminalPollTimer);
  terminalPollTimer = setInterval(() => pollTerminal(), 2000);
}

function closeTerminal() {
  terminalOverlay.classList.add("hidden");
  terminalAgent = null;
  if (terminalPollTimer) { clearInterval(terminalPollTimer); terminalPollTimer = null; }
}

async function loadTerminal(agent) {
  const lines = await fetchTerminal(agent);
  if (lines.length > 0) {
    terminalLines = lines;
  } else {
    terminalLines = [
      { type: "system", text: `[session:${agent}] connected` },
      { type: "system", text: `[session:${agent}] waiting for output...` },
    ];
  }
  renderTerminal();
}

async function pollTerminal() {
  if (!terminalAgent) return;
  const lines = await fetchTerminal(terminalAgent);
  if (lines.length > terminalLines.length) {
    terminalLines = lines;
    renderTerminal();
  }
  if (fleetData?.agents?.[terminalAgent]) {
    const state = fleetData.agents[terminalAgent].state || "idle";
    terminalAgentState.className = `agent-state state-${state}`;
    terminalAgentState.textContent = state;
  }
}

function renderTerminal() {
  terminalOutput.innerHTML = terminalLines.map(line => {
    const cls = line.type || "text";
    if (cls === "assistant" || cls === "text") {
      const html = md.parse(line.text || "");
      return `<div class="term-line"><div class="term-md">${html}</div></div>`;
    }
    return `<div class="term-line ${cls}">${esc(line.text || "")}</div>`;
  }).join("");

  terminalOutput.querySelectorAll("pre code").forEach(block => {
    if (window.hljs) hljs.highlightElement(block);
  });
  terminalOutput.scrollTop = terminalOutput.scrollHeight;
}

async function handleTerminalSend() {
  const content = terminalInput.value.trim();
  if (!content || !terminalAgent) return;
  terminalLines.push({ type: "user-msg", text: `[you] ${content}` });
  renderTerminal();
  terminalInput.value = "";
  await sendToAgent(terminalAgent, content);
}

// Terminal events
terminalClose.addEventListener("click", closeTerminal);
terminalOverlay.addEventListener("click", e => { if (e.target === terminalOverlay) closeTerminal(); });
terminalSendBtn.addEventListener("click", handleTerminalSend);
terminalInput.addEventListener("keydown", e => {
  if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handleTerminalSend(); }
});
document.addEventListener("keydown", e => {
  if (e.key === "Escape" && !terminalOverlay.classList.contains("hidden")) closeTerminal();
});

// ============================================================
// Spawn Modal
// ============================================================

const spawnModal = document.getElementById("spawn-modal");
const spawnBtn = document.getElementById("spawn-btn");
const spawnClose = document.getElementById("spawn-modal-close");
const spawnCancel = document.getElementById("spawn-cancel");
const spawnConfirm = document.getElementById("spawn-confirm");
const spawnAgentSelect = document.getElementById("spawn-agent-select");
const spawnMachineSelect = document.getElementById("spawn-machine-select");
const spawnStatus = document.getElementById("spawn-status");

async function openSpawnModal() {
  spawnStatus.textContent = "";
  spawnStatus.className = "spawn-status";

  // Fetch available agents and machines
  try {
    const res = await fetch("/api/agents/available");
    const data = await res.json();

    spawnAgentSelect.innerHTML = data.agents
      .sort((a, b) => a.name.localeCompare(b.name))
      .map(a => `<option value="${esc(a.name)}" data-machine="${esc(a.machine)}">${esc(a.name)} (${a.state})</option>`)
      .join("");

    spawnMachineSelect.innerHTML = data.machines
      .sort()
      .map(m => `<option value="${esc(m)}"${m === data.local_machine ? " selected" : ""}>${esc(m)}${m === data.local_machine ? " (local)" : ""}</option>`)
      .join("");

    // Auto-select the agent's configured machine
    spawnAgentSelect.addEventListener("change", () => {
      const opt = spawnAgentSelect.selectedOptions[0];
      const machine = opt?.dataset.machine;
      if (machine) spawnMachineSelect.value = machine;
    });
    // Trigger for initial selection
    const initOpt = spawnAgentSelect.selectedOptions[0];
    if (initOpt?.dataset.machine) spawnMachineSelect.value = initOpt.dataset.machine;

  } catch {
    spawnAgentSelect.innerHTML = '<option>Failed to load</option>';
    spawnMachineSelect.innerHTML = '<option>Failed to load</option>';
  }

  spawnModal.classList.remove("hidden");
}

function closeSpawnModal() {
  spawnModal.classList.add("hidden");
}

async function confirmSpawn() {
  const agent = spawnAgentSelect.value;
  const machine = spawnMachineSelect.value;
  if (!agent) return;

  spawnConfirm.disabled = true;
  spawnConfirm.textContent = "Spawning...";
  spawnStatus.textContent = "";

  try {
    const res = await fetch("/api/spawn", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent, machine }),
    });
    const data = await res.json();

    if (res.ok) {
      spawnStatus.textContent = `${agent} is ${data.status || "spawning"} on ${machine}`;
      spawnStatus.className = "spawn-status success";
      // Refresh fleet after short delay
      setTimeout(() => { poll(); closeSpawnModal(); }, 1200);
    } else {
      spawnStatus.textContent = data.error || `Failed: HTTP ${res.status}`;
      spawnStatus.className = "spawn-status error";
    }
  } catch (e) {
    spawnStatus.textContent = `Error: ${e.message}`;
    spawnStatus.className = "spawn-status error";
  } finally {
    spawnConfirm.disabled = false;
    spawnConfirm.textContent = "Spawn";
  }
}

spawnBtn.addEventListener("click", openSpawnModal);
spawnClose.addEventListener("click", closeSpawnModal);
spawnCancel.addEventListener("click", closeSpawnModal);
spawnConfirm.addEventListener("click", confirmSpawn);
spawnModal.addEventListener("click", e => { if (e.target === spawnModal) closeSpawnModal(); });
document.addEventListener("keydown", e => {
  if (e.key === "Escape" && !spawnModal.classList.contains("hidden")) closeSpawnModal();
});

// ============================================================
// Poll + Animation Loop
// ============================================================

async function poll() {
  const fleet = await fetchFleet();
  if (fleet) renderFleet(fleet);
}

function animate() {
  animFrame++;
  if (fleetData) renderMachineMap(fleetData);
  requestAnimationFrame(animate);
}

function esc(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}

// --- Init ---
renderFleet(null);
poll();
setInterval(poll, POLL_INTERVAL);
requestAnimationFrame(animate);
window.addEventListener("resize", () => { if (fleetData) renderMachineMap(fleetData); });
