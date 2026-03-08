// WRAI.TH Mission Control — Main Application

const API = window.location.origin;
let ws = null;
let currentAgent = null;
let fleetData = {};
let stdoutBuffers = {}; // agent -> array of lines

// ── Boot ────────────────────────────────────────────────

async function init() {
    await fetchFleet();
    connectWebSocket();
    setInterval(fetchFleet, 10000); // refresh fleet every 10s
}

// ── Fleet ───────────────────────────────────────────────

async function fetchFleet() {
    try {
        const resp = await fetch(`${API}/api/fleet`);
        fleetData = await resp.json();
        renderFleet();
        updateRelayStatus(true);
    } catch (e) {
        updateRelayStatus(false);
    }
}

function renderFleet() {
    const grid = document.getElementById('fleet-grid');
    grid.innerHTML = '';

    const machines = fleetData.machines || {};
    for (const [machine, agents] of Object.entries(machines)) {
        // Machine label
        const label = document.createElement('div');
        label.className = 'machine-label';
        label.textContent = machine;
        label.style.width = '100%';
        grid.appendChild(label);

        // Agent cards
        for (const [name, info] of Object.entries(agents)) {
            const card = document.createElement('div');
            card.className = 'fleet-card';
            card.dataset.state = info.state;
            card.onclick = () => selectAgent(name);

            const stateSymbol = {
                'working': '\u25CF',
                'idle': '\u25CB',
                'spawning': '\u25D0',
                'sleeping': '\u25D1',
                'crashed': '\u2716',
                'dead': '\u2716',
            }[info.state] || '\u25CB';

            card.innerHTML = `
                <div class="agent-name">
                    <span class="state-indicator">${stateSymbol}</span> ${name}
                </div>
                <div class="agent-status">${info.state} ${info.pid ? '| PID ' + info.pid : ''}</div>
            `;
            grid.appendChild(card);
        }
    }
}

// ── Agent Detail ────────────────────────────────────────

async function selectAgent(name) {
    currentAgent = name;

    // Show panel
    document.getElementById('agent-panel').classList.remove('hidden');

    // Update tabs
    renderTabs();

    // Fetch agent detail
    try {
        const resp = await fetch(`${API}/api/agent/${name}`);
        const data = await resp.json();
        renderAgentDetail(data);
    } catch (e) {
        console.error('Failed to fetch agent:', e);
    }
}

function renderTabs() {
    const bar = document.getElementById('tab-bar');
    bar.innerHTML = '';

    const machines = fleetData.machines || {};
    for (const agents of Object.values(machines)) {
        for (const name of Object.keys(agents)) {
            const tab = document.createElement('div');
            tab.className = 'tab' + (name === currentAgent ? ' active' : '');
            tab.textContent = name;
            tab.onclick = () => selectAgent(name);
            bar.appendChild(tab);
        }
    }
}

function renderAgentDetail(data) {
    document.getElementById('agent-name').textContent = data.name;

    const stateBadge = document.getElementById('agent-state');
    stateBadge.textContent = data.state;
    stateBadge.style.background = stateColor(data.state, 0.15);
    stateBadge.style.color = stateColor(data.state, 1);
    stateBadge.style.border = `1px solid ${stateColor(data.state, 0.3)}`;

    document.getElementById('agent-pid').textContent = data.pid ? `PID: ${data.pid}` : '';
    document.getElementById('agent-uptime').textContent = data.uptime ? formatUptime(data.uptime) : '';

    // Render stdout history
    const stdout = document.getElementById('stdout-output');
    const lines = data.output || stdoutBuffers[data.name] || [];
    stdout.textContent = lines.join('\n');
    stdout.scrollTop = stdout.scrollHeight;
}

function stateColor(state, alpha) {
    const colors = {
        'working': `rgba(0, 255, 136, ${alpha})`,
        'idle': `rgba(104, 104, 168, ${alpha})`,
        'spawning': `rgba(255, 170, 0, ${alpha})`,
        'sleeping': `rgba(255, 107, 157, ${alpha})`,
        'crashed': `rgba(255, 68, 68, ${alpha})`,
        'dead': `rgba(255, 68, 68, ${alpha})`,
    };
    return colors[state] || `rgba(200, 200, 232, ${alpha})`;
}

function formatUptime(seconds) {
    if (seconds < 60) return `${Math.floor(seconds)}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
    return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}

// ── WebSocket ───────────────────────────────────────────

function connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${protocol}//${window.location.host}/ws`);

    ws.onopen = () => updateWsStatus(true);
    ws.onclose = () => {
        updateWsStatus(false);
        setTimeout(connectWebSocket, 3000);
    };
    ws.onerror = () => updateWsStatus(false);

    ws.onmessage = (event) => {
        const data = JSON.parse(event.data);
        if (data.type === 'stdout') {
            handleStdout(data.agent, data.line);
        } else if (data.type === 'status') {
            handleStatusUpdate(data.agent, data.state);
        }
    };
}

function handleStdout(agent, line) {
    if (!stdoutBuffers[agent]) stdoutBuffers[agent] = [];
    stdoutBuffers[agent].push(line);
    // Keep last 500 lines
    if (stdoutBuffers[agent].length > 500) {
        stdoutBuffers[agent] = stdoutBuffers[agent].slice(-500);
    }

    // Update UI if this agent is selected
    if (agent === currentAgent) {
        const stdout = document.getElementById('stdout-output');
        stdout.textContent += line + '\n';
        stdout.scrollTop = stdout.scrollHeight;
    }
}

function handleStatusUpdate(agent, state) {
    // Update fleet data
    const machines = fleetData.machines || {};
    for (const agents of Object.values(machines)) {
        if (agents[agent]) {
            agents[agent].state = state;
        }
    }
    renderFleet();

    if (agent === currentAgent) {
        const badge = document.getElementById('agent-state');
        badge.textContent = state;
        badge.style.background = stateColor(state, 0.15);
        badge.style.color = stateColor(state, 1);
    }
}

// ── Status Indicators ───────────────────────────────────

function updateRelayStatus(online) {
    const el = document.getElementById('relay-status');
    el.className = 'status-dot ' + (online ? 'online' : 'offline');
}

function updateWsStatus(online) {
    const el = document.getElementById('ws-status');
    el.className = 'status-dot ' + (online ? 'online' : 'offline');
}

// ── Actions ─────────────────────────────────────────────

async function agentAction(action) {
    if (!currentAgent) return;
    try {
        await fetch(`${API}/api/agent/${currentAgent}/${action}`, { method: 'POST' });
        await fetchFleet();
        await selectAgent(currentAgent);
    } catch (e) {
        console.error('Action failed:', e);
    }
}

window.agentAction = agentAction;

async function sendMessage() {
    if (!currentAgent) return;
    const input = document.getElementById('send-input');
    const content = input.value.trim();
    if (!content) return;

    try {
        await fetch(`${API}/api/send`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                as: 'controller',
                to: currentAgent,
                content: content,
                subject: content.split(' ').slice(0, 5).join(' '),
            }),
        });
        input.value = '';
    } catch (e) {
        console.error('Send failed:', e);
    }
}

window.sendMessage = sendMessage;

// Enter key to send
document.addEventListener('DOMContentLoaded', () => {
    const input = document.getElementById('send-input');
    if (input) {
        input.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') sendMessage();
        });
    }
});

// ── Init ────────────────────────────────────────────────

init();
