import { CanvasEngine } from "./canvas.js";
import { WorldBackground, World } from "./world.js";
import { AgentView } from "./agent-view.js";
import { APIClient } from "./api-client.js";
import { MessageOrb } from "./message-orb.js";
import { KanbanBoard } from "./kanban.js";
import { ShortcutManager } from "./shortcuts.js";
import { ConnectionOverlay } from "./connections.js";

// --- Composite key for cross-project agent identity ---
function agentKey(project, name) {
  return `${project}:${name}`;
}

// DOM elements
const canvas = document.getElementById("relay-canvas");
const statusDot = document.getElementById("status-dot");
const agentCountEl = document.getElementById("agent-count");
const messagesTitle = document.getElementById("messages-title");
const messagesList = document.getElementById("messages-list");
const detailPanel = document.getElementById("agent-detail");
const detailName = document.getElementById("detail-name");
const detailRole = document.getElementById("detail-role");
const detailDesc = document.getElementById("detail-desc");
const detailProject = document.getElementById("detail-project");
const detailStatus = document.getElementById("detail-status");
const detailLastSeen = document.getElementById("detail-last-seen");
const detailRegistered = document.getElementById("detail-registered");
const detailClose = document.getElementById("detail-close");
const detailReportsTo = document.getElementById("detail-reports-to");
const detailDirectReports = document.getElementById("detail-direct-reports");
const userQuestionsPanel = document.getElementById("user-questions");

// State
const engine = new CanvasEngine(canvas);
const worldBg = new WorldBackground();
const world = new World();
const agentViews = new Map();      // "project:name" -> AgentView
let projectGroups = new Map();      // project -> Set<agentKey>
let conversations = [];             // cached conversation list
let focusedAgent = null;            // "project:name" of focused agent, or null
let focusedProject = null;          // project name when zoomed into a cluster, or null
let focusedTeam = null;             // { project, slug, members: [agentName] } when viewing a team
let paletteCounter = 0;
let agentsData = [];                // cached raw agent data for hierarchy
let teamsData = [];                 // cached teams with members
let connected = false;
let firstLayout = true;
let hoveredAgentKey = null;

const connectionOverlay = new ConnectionOverlay();

engine.add(worldBg);
engine.add(connectionOverlay);
engine.add(world);
engine.start();

// --- Cluster layout ---

function layoutAgents() {
  const projects = [...projectGroups.keys()].sort();
  const count = agentViews.size;
  if (count === 0) {
    world.clusters = [];
    return;
  }

  // World-space origin
  const cx = engine.width / 2;
  const cy = engine.height / 2;

  if (projects.length <= 1) {
    // --- Single project: team-aware layout ---
    const project = projects[0] || "default";
    const keys = projectGroups.get(project) || new Set();
    const agentCount = keys.size;

    // Build team clusters from agentsData
    const teamClusters = new Map(); // teamSlug -> Set<agentKey>
    const agentPrimaryTeam = new Map(); // agentKey -> teamSlug
    const placed = new Set();

    for (const a of agentsData) {
      const ap = a.project || "default";
      if (ap !== project) continue;
      const key = agentKey(ap, a.name);
      if (!a.teams || a.teams.length === 0) continue;
      // Primary team = first team (prefer non-admin for layout grouping)
      const primary = a.teams.find(t => t.type !== "admin") || a.teams[0];
      agentPrimaryTeam.set(key, primary.slug);
      for (const t of a.teams) {
        if (!teamClusters.has(t.slug)) teamClusters.set(t.slug, new Set());
        teamClusters.get(t.slug).add(key);
      }
    }

    // Agents with no team go to "ungrouped"
    for (const key of keys) {
      if (!agentPrimaryTeam.has(key)) {
        agentPrimaryTeam.set(key, "__ungrouped");
        if (!teamClusters.has("__ungrouped")) teamClusters.set("__ungrouped", new Set());
        teamClusters.get("__ungrouped").add(key);
      }
    }

    // Group agents by their PRIMARY team for layout
    const primaryGroups = new Map(); // teamSlug -> [agentKey]
    for (const key of keys) {
      const team = agentPrimaryTeam.get(key) || "__ungrouped";
      if (!primaryGroups.has(team)) primaryGroups.set(team, []);
      primaryGroups.get(team).push(key);
    }

    const groupList = [...primaryGroups.entries()].filter(([, members]) => members.length > 0);
    const numGroups = groupList.length;

    if (numGroups <= 1 && agentCount <= 3) {
      // Very few agents — simple centered layout
      let i = 0;
      const radius = agentCount > 1 ? Math.min(engine.width, engine.height) * 0.20 : 0;
      for (const key of keys) {
        const av = agentViews.get(key);
        if (!av) continue;
        if (agentCount === 1) {
          av.targetX = cx;
          av.targetY = cy;
        } else {
          const angle = -Math.PI / 2 + (i / agentCount) * Math.PI * 2;
          av.targetX = cx + Math.cos(angle) * radius;
          av.targetY = cy + Math.sin(angle) * radius;
        }
        i++;
      }
    } else {
      // Team-based cluster layout
      // Place team groups on a circle, agents in each group in a small sub-circle
      const outerR = Math.min(engine.width, engine.height) * 0.28;
      const maxSubR = outerR * 0.35;

      for (let gi = 0; gi < groupList.length; gi++) {
        const [, members] = groupList[gi];
        const groupAngle = -Math.PI / 2 + (gi / numGroups) * Math.PI * 2;
        const gcx = cx + Math.cos(groupAngle) * outerR;
        const gcy = cy + Math.sin(groupAngle) * outerR;

        if (members.length === 1) {
          const av = agentViews.get(members[0]);
          if (av) { av.targetX = gcx; av.targetY = gcy; }
        } else {
          const subR = Math.min(maxSubR, 50 + members.length * 20);
          for (let mi = 0; mi < members.length; mi++) {
            const av = agentViews.get(members[mi]);
            if (!av) continue;
            const mAngle = -Math.PI / 2 + (mi / members.length) * Math.PI * 2;
            av.targetX = gcx + Math.cos(mAngle) * subR;
            av.targetY = gcy + Math.sin(mAngle) * subR;
          }
        }
      }
    }

    world.clusters = [{ project, cx, cy, radius: Math.min(engine.width, engine.height) * 0.42, hidden: true }];

    // Neutralize camera — positions are screen-space
    engine.camera.snapTo(cx, cy, 1.0);
  } else {
    // --- Multiple projects: clusters on a large circle ---
    // Each cluster's inner radius fills proportionally
    const clusterData = projects.map(project => {
      const keys = projectGroups.get(project) || new Set();
      const agentCount = keys.size;
      // Inner radius: at least 120px spacing between agents, min 80
      const minBySpacing = agentCount > 1 ? (120 * agentCount) / (2 * Math.PI) : 0;
      const innerRadius = Math.max(minBySpacing, 80);
      return { project, keys, agentCount, innerRadius };
    });

    const maxClusterRadius = Math.max(...clusterData.map(c => c.innerRadius));
    const outerRadius = Math.max(maxClusterRadius * 2.5 + 200,
      (300 * projects.length) / (2 * Math.PI));

    const clusters = [];
    for (let pi = 0; pi < clusterData.length; pi++) {
      const { project, keys, agentCount, innerRadius } = clusterData[pi];

      const outerAngle = -Math.PI / 2 + (pi / projects.length) * Math.PI * 2;
      const clusterCx = cx + Math.cos(outerAngle) * outerRadius;
      const clusterCy = cy + Math.sin(outerAngle) * outerRadius;

      let i = 0;
      for (const key of keys) {
        const av = agentViews.get(key);
        if (!av) continue;
        if (agentCount === 1) {
          av.targetX = clusterCx;
          av.targetY = clusterCy;
        } else {
          const angle = -Math.PI / 2 + (i / agentCount) * Math.PI * 2;
          av.targetX = clusterCx + Math.cos(angle) * innerRadius;
          av.targetY = clusterCy + Math.sin(angle) * innerRadius;
        }
        i++;
      }

      clusters.push({ project, cx: clusterCx, cy: clusterCy, radius: innerRadius + 60 });
    }

    world.clusters = clusters;

    // Fit camera to show all clusters
    if (focusedProject) {
      const cluster = clusters.find(c => c.project === focusedProject);
      if (cluster) fitToCluster(cluster);
      else fitToAllClusters();
    } else {
      fitToAllClusters();
    }
  }
}

/** Smoothly fit camera to show a single cluster (multi-project zoom). */
function fitToCluster(cluster) {
  const diam = (cluster.radius + 40) * 2;
  const zoomX = (engine.width * 0.8) / diam;
  const zoomY = (engine.height * 0.8) / diam;
  const zoom = Math.max(0.15, Math.min(zoomX, zoomY));

  if (firstLayout) {
    engine.camera.snapTo(cluster.cx, cluster.cy, zoom);
    firstLayout = false;
  } else {
    engine.camera.lookAt(cluster.cx, cluster.cy, zoom);
  }
}

/** Smoothly fit camera to show all clusters (multi-project overview). */
function fitToAllClusters() {
  const clusters = world.clusters;
  if (clusters.length === 0) return;

  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const c of clusters) {
    minX = Math.min(minX, c.cx - c.radius);
    minY = Math.min(minY, c.cy - c.radius);
    maxX = Math.max(maxX, c.cx + c.radius);
    maxY = Math.max(maxY, c.cy + c.radius);
  }

  const contentW = maxX - minX || 1;
  const contentH = maxY - minY || 1;
  const centerX = (minX + maxX) / 2;
  const centerY = (minY + maxY) / 2;
  const zoomX = (engine.width * 0.85) / contentW;
  const zoomY = (engine.height * 0.85) / contentH;
  const zoom = Math.max(0.15, Math.min(zoomX, zoomY));

  if (firstLayout) {
    engine.camera.snapTo(centerX, centerY, zoom);
    firstLayout = false;
  } else {
    engine.camera.lookAt(centerX, centerY, zoom);
  }
}

// --- API callbacks ---

function onAgents(agents) {
  if (!connected) {
    connected = true;
    statusDot.classList.add("connected");
  }

  agentCountEl.textContent = `${agents.length} agent${agents.length !== 1 ? "s" : ""}`;

  const currentKeys = new Set(agents.map(a => agentKey(a.project || "default", a.name)));

  // Remove agents that no longer exist
  for (const [key, av] of agentViews) {
    if (!currentKeys.has(key)) {
      engine.remove(av);
      agentViews.delete(key);
    }
  }

  // Rebuild project groups
  projectGroups = new Map();

  // Add/update agents
  for (const a of agents) {
    const project = a.project || "default";
    const key = agentKey(project, a.name);

    // Track project groups
    if (!projectGroups.has(project)) projectGroups.set(project, new Set());
    projectGroups.get(project).add(key);

    let av = agentViews.get(key);
    if (!av) {
      av = new AgentView(a.name, a.role, a.description, paletteCounter++, a.online, project);
      av.setPosition(engine.width / 2, engine.height / 2);
      av.spawnEffect();
      agentViews.set(key, av);
      engine.add(av);
    } else {
      av.online = a.online;
      av.role = a.role;
      av.description = a.description;
    }
    av._reportsTo = a.reports_to || null;
    av._lastSeenRaw = a.last_seen;
    av._registeredRaw = a.registered_at;
    av.isExecutive = a.is_executive || false;
    av._teams = a.teams || [];
    av.session_id = a.session_id || null;
    av.sleeping = a.status === "sleeping";
    // Apply activity from agents API (enriched by ingester)
    if (a.activity && a.activity !== "idle") {
      av.activity = a.activity;
      av.activityTool = a.activity_tool || "";
    }
  }

  // Update agent task labels from current tasks
  updateAgentTaskLabels();

  // Update connection overlay with current teams
  updateConnectionOverlay();

  agentsData = agents;

  // Only show project tags when there are multiple projects
  const multiProject = projectGroups.size > 1;
  for (const [, av] of agentViews) {
    av.showProjectTag = multiProject;
  }

  layoutAgents();
  updateHighlights();
  updateHierarchyLinks();
}

function onActivity(sessions, sseAgents) {
  activitySessions = sessions;

  // If we have enriched agent data from SSE, apply statuses directly
  if (sseAgents) {
    for (const sa of sseAgents) {
      const key = agentKey(sa.project, sa.name);
      const av = agentViews.get(key);
      if (!av) continue;

      // Apply status
      av.sleeping = sa.status === "sleeping";
      av.online = sa.status === "busy" || sa.status === "active";
      av._sseStatus = sa.status; // busy, active, sleeping, inactive, deleted

      // Apply activity
      if (sa.activity && sa.activity !== "idle") {
        applyActivity(av, { activity: sa.activity, tool: sa.activity_tool, file: "" });
      } else {
        av.activity = null;
        av.activityTool = "";
      }
    }
  }

  // Handle ghost sprites for unmatched sessions
  const matchedSessionIDs = new Set();
  if (sseAgents) {
    for (const sa of sseAgents) {
      if (sa.session_id) matchedSessionIDs.add(sa.session_id);
    }
  }

  for (const s of sessions) {
    if (matchedSessionIDs.has(s.session_id)) continue;

    // Unmatched session → ghost sprite
    const ghostName = "session:" + s.session_id.slice(0, 8);
    if (!agentViews.has(ghostName)) {
      const av = new AgentView(ghostName, s.tool || "unknown", "", paletteCounter++, true, "default");
      av.session_id = s.session_id;
      av._isGhost = true;
      av.setPosition(
        engine.width / 4 + Math.random() * engine.width / 2,
        engine.height / 4 + Math.random() * engine.height / 2,
      );
      av.spawnEffect();
      agentViews.set(ghostName, av);
      engine.add(av);
    }
    const av = agentViews.get(ghostName);
    av.role = s.tool || av.role;
    applyActivity(av, s);
  }

  // Remove ghost sprites for sessions that disappeared
  for (const [key, av] of agentViews) {
    if (av._isGhost && !sessions.find(s => s.session_id === av.session_id)) {
      engine.remove(av);
      agentViews.delete(key);
    }
  }
}

function applyActivity(av, s) {
  const prevActivity = av.activity;
  av.activity = s.activity || null;
  av.activityTool = s.tool || "";
  av.activityFile = s.file || "";
  if (prevActivity !== av.activity && av.activity && av.activity !== "idle") {
    const glowColor = ACTIVITY_GLOW[av.activity];
    if (glowColor && av.particles) av.particles.emitActivity(av.x, av.y + 24, glowColor);
  }
}

function onConversations(convs) {
  conversations = convs;
  updateConvFilterOptions();
}

function onNewMessages(msgs) {
  checkForUserMessages(msgs);

  for (const msg of msgs) {
    const msgProject = msg.project || "default";
    const fromKey = agentKey(msgProject, msg.from);
    const fromAv = agentViews.get(fromKey);

    if (fromAv) {
      const preview = msg.subject || msg.content.slice(0, 80);
      fromAv.showBubble(preview, "speech");
    }

    const msgKind = msg.type || "default";

    if (fromAv && msg.to && msg.to.startsWith("team:")) {
      // Team-addressed message — send orbs to all team members
      const teamSlug = msg.to.slice(5);
      const teamMembers = getTeamMemberKeys(msgProject, teamSlug);
      for (const memberKey of teamMembers) {
        if (memberKey !== fromKey) {
          const targetAv = agentViews.get(memberKey);
          if (targetAv) {
            const orb = new MessageOrb(
              fromAv.x, fromAv.y,
              targetAv.x, targetAv.y,
              msgKind,
              () => { engine.remove(orb); targetAv.arrivalBurst(msgKind); }
            );
            engine.add(orb);
          }
        }
      }
    } else if (fromAv && msg.to && msg.to !== "*") {
      const toKey = agentKey(msgProject, msg.to);
      const toAv = agentViews.get(toKey);
      if (toAv) {
        const orb = new MessageOrb(
          fromAv.x, fromAv.y,
          toAv.x, toAv.y,
          msgKind,
          () => { engine.remove(orb); toAv.arrivalBurst(msgKind); }
        );
        engine.add(orb);
      }
    } else if (fromAv && msg.to === "*") {
      for (const [key, av] of agentViews) {
        if (key !== fromKey) {
          const orb = new MessageOrb(
            fromAv.x, fromAv.y,
            av.x, av.y,
            msg.type || "notification",
            () => { engine.remove(orb); av.arrivalBurst(msg.type || "notification"); }
          );
          engine.add(orb);
        }
      }
    } else if (fromAv && msg.conversation_id) {
      const conv = conversations.find(c => c.id === msg.conversation_id);
      if (conv && conv.members) {
        for (const member of conv.members) {
          if (member !== msg.from) {
            const targetKey = agentKey(msgProject, member);
            const targetAv = agentViews.get(targetKey);
            if (targetAv) {
              const orb = new MessageOrb(
                fromAv.x, fromAv.y,
                targetAv.x, targetAv.y,
                msgKind,
                () => { engine.remove(orb); targetAv.arrivalBurst(msgKind); }
              );
              engine.add(orb);
            }
          }
        }
      }
    }

    // Append to messages panel respecting focus context (with typewriter for live)
    if (currentMode !== "kanban") {
      let show = false;
      if (focusedAgent) {
        const focusAv = agentViews.get(focusedAgent);
        show = focusAv && msgProject === focusAv.project &&
            (msg.from === focusAv.name || msg.to === focusAv.name);
      } else if (focusedTeam) {
        const memberSet = new Set(focusedTeam.members);
        show = msgProject === focusedTeam.project && (memberSet.has(msg.from) || memberSet.has(msg.to));
      } else if (focusedProject) {
        show = msgProject === focusedProject;
      } else {
        show = true;
      }
      if (show) appendMessage(msg, false, true);
    }
  }
}

// --- Focus / Highlights ---

function updateHighlights() {
  const convId = msgConvFilter ? msgConvFilter.value : "";

  if (!convId) {
    // No filter — show all
    for (const [, av] of agentViews) {
      av.highlighted = true;
      av.dimMode = false;
    }
    return;
  }

  // Find conversation members
  const conv = conversations.find(c => c.id === convId);
  const members = conv ? (conv.members || []) : [];
  const memberSet = new Set(members);

  for (const [key, av] of agentViews) {
    const inConv = memberSet.has(av.name);
    av.highlighted = inConv;
    av.dimMode = !inConv;
  }
}

/** Filter messages by current view context. */
function filterMessagesByView(msgs) {
  if (focusedAgent) {
    const av = agentViews.get(focusedAgent);
    if (!av) return [];
    messagesTitle.textContent = av.name;
    return msgs.filter(m => {
      const mp = m.project || "default";
      return mp === av.project && (m.from === av.name || m.to === av.name);
    });
  } else if (focusedTeam) {
    messagesTitle.textContent = `team: ${focusedTeam.slug}`;
    const memberSet = new Set(focusedTeam.members);
    return msgs.filter(m => {
      const mp = m.project || "default";
      return mp === focusedTeam.project && (memberSet.has(m.from) || memberSet.has(m.to));
    });
  } else if (focusedProject) {
    messagesTitle.textContent = focusedProject;
    return msgs.filter(m => (m.project || "default") === focusedProject);
  } else {
    messagesTitle.textContent = "All Messages";
    return msgs;
  }
}

async function loadMessages() {
  const allMsgs = await client.fetchAllMessagesAllProjects();
  const filtered = filterMessagesByView(allMsgs);

  // Build set of wanted msg IDs
  const wantedIds = new Set(filtered.map(m => m.id));

  // Remove stale items
  messagesList.querySelectorAll(".msg-item[data-msg-id]").forEach(el => {
    if (!wantedIds.has(el.dataset.msgId)) el.remove();
  });

  // Track existing
  const existingIds = new Set();
  messagesList.querySelectorAll(".msg-item[data-msg-id]").forEach(el => {
    existingIds.add(el.dataset.msgId);
  });

  // Remove empty placeholder
  const empty = messagesList.querySelector(".msg-empty");
  if (empty) empty.remove();

  if (filtered.length === 0) {
    messagesList.innerHTML = '<div class="msg-empty">No messages yet</div>';
    return;
  }

  for (const msg of filtered) {
    if (!existingIds.has(msg.id)) {
      appendMessage(msg);
    }
  }
  messagesList.scrollTop = messagesList.scrollHeight;
}

function appendMessage(msg, showConv = false, useTypewriter = false) {
  const el = document.createElement("div");
  el.className = "msg-item";
  el.dataset.msgId = msg.id;
  if (msg.conversation_id) el.dataset.convId = msg.conversation_id;

  const time = formatTime(msg.created_at);
  const subject = msg.subject ? `<span class="msg-subject">${escapeHtml(msg.subject)}</span>` : "";
  const content = msg.content.length > 500 ? msg.content.slice(0, 497) + "..." : msg.content;

  let convTag = "";
  if (msg.conversation_id) {
    const conv = conversations.find(c => c.id === msg.conversation_id);
    const convName = conv ? conv.title : "conv";
    convTag = `<span class="msg-conv-tag">${escapeHtml(convName)}</span> `;
  }

  // Show project tag for cross-project view
  let projectTag = "";
  const msgProject = msg.project || "default";
  if (projectGroups.size > 1 && msgProject !== "default") {
    projectTag = `<span class="msg-conv-tag">${escapeHtml(msgProject)}</span> `;
  }

  // Team-addressed messages get a team tag
  let toTag = "";
  if (msg.to && msg.to.startsWith("team:")) {
    const teamSlug = msg.to.slice(5);
    toTag = `<span class="msg-team-tag">→ team:${escapeHtml(teamSlug)}</span> `;
  } else if (msg.to && msg.to === "*") {
    toTag = `<span class="msg-team-tag">→ broadcast</span> `;
  } else if (msg.to) {
    toTag = `<span class="msg-to">→ ${escapeHtml(msg.to)}</span> `;
  }

  el.innerHTML = `
    ${subject}
    ${projectTag}${convTag}<span class="msg-from">${escapeHtml(msg.from)}</span> ${toTag}
    <span class="msg-content">${useTypewriter ? "" : escapeHtml(content)}</span>
    <div class="msg-time">${time}</div>
  `;

  messagesList.appendChild(el);
  messagesList.scrollTop = messagesList.scrollHeight;

  // Typewriter effect for new live messages
  if (useTypewriter && content.length > 0) {
    typewriterAppend(el, content, 12);
  }
}

// --- Hierarchy links ---

function updateHierarchyLinks() {
  const links = [];
  for (const [, av] of agentViews) {
    if (av._reportsTo) {
      // Look up manager within the same project
      const managerKey = agentKey(av.project, av._reportsTo);
      const managerAv = agentViews.get(managerKey);
      if (managerAv) {
        links.push({ from: managerAv, to: av });
      }
    }
  }
  world.hierarchyLinks = links;
}

// --- User notification inbox ---
// Catches all messages sent to "user" (questions, notifications, responses, tasks, etc.)

const shownUserMsgs = new Set();
const _celebratedTasks = new Set();

function checkForUserMessages(msgs) {
  for (const msg of msgs) {
    if (shownUserMsgs.has(msg.id)) continue;
    // Show if explicitly to "user" or if type is user_question
    const isForUser = (msg.to === "user") || (msg.type === "user_question");
    if (!isForUser) continue;
    shownUserMsgs.add(msg.id);
    showUserCard(msg);
  }
}

function userMsgCategory(msg) {
  const t = msg.type || "";
  if (t === "user_question" || t === "question") return "question";
  if (t === "notification") return "notification";
  if (t === "task") return "task";
  return "response"; // response, code-snippet, or any other
}

function userMsgTypeLabel(cat) {
  switch (cat) {
    case "question": return "Question";
    case "notification": return "Notification";
    case "task": return "Task";
    default: return "Response";
  }
}

function showUserCard(msg) {
  const cat = userMsgCategory(msg);
  const card = document.createElement("div");
  card.className = `uq-card uq-card--${cat}`;
  card.dataset.msgId = msg.id;

  const fromLabel = msg.from || "agent";
  const subject = msg.subject || "";
  const content = msg.content || "";
  const msgProject = msg.project || "default";
  const needsReply = cat === "question";

  let html = `
    <div class="uq-header">
      <span class="uq-from">${escapeHtml(fromLabel)}</span>
      <span class="uq-type">${userMsgTypeLabel(cat)}</span>
    </div>
    <button class="uq-dismiss">&times;</button>
  `;

  if (subject) html += `<div class="uq-subject">${escapeHtml(subject)}</div>`;
  if (content) html += `<div class="uq-content">${escapeHtml(content)}</div>`;

  if (needsReply) {
    html += `
      <textarea placeholder="Type your response..."></textarea>
      <button class="uq-respond-btn">Respond</button>
    `;
  }

  card.innerHTML = html;

  // Dismiss button
  card.querySelector(".uq-dismiss").addEventListener("click", () => {
    card.style.opacity = "0";
    card.style.transition = "opacity 0.3s ease";
    setTimeout(() => card.remove(), 300);
  });

  // Auto-dismiss notifications after 15s
  if (cat === "notification") {
    setTimeout(() => {
      if (card.parentNode) {
        card.style.opacity = "0";
        card.style.transition = "opacity 0.5s ease";
        setTimeout(() => card.remove(), 500);
      }
    }, 15000);
  }

  // Reply handling for questions
  if (needsReply) {
    const textarea = card.querySelector("textarea");
    const button = card.querySelector(".uq-respond-btn");

    button.addEventListener("click", async () => {
      const response = textarea.value.trim();
      if (!response) return;
      button.disabled = true;
      button.textContent = "Sending...";

      const ok = await client.sendUserResponse(msgProject, msg.from, response, msg.id);
      if (ok) {
        card.style.opacity = "0";
        card.style.transition = "opacity 0.3s ease";
        setTimeout(() => card.remove(), 300);
      } else {
        button.disabled = false;
        button.textContent = "Respond";
      }
    });
  }

  userQuestionsPanel.appendChild(card);
}

function showUserTaskCard(task) {
  const card = document.createElement("div");
  const prio = task.priority || "P2";
  const isP0 = prio === "P0";
  card.className = `uq-card uq-card--task${isP0 ? " uq-card--task-p0" : ""}`;
  card.dataset.taskId = task.id;

  const from = task.dispatched_by || "agent";
  const prioIcon = isP0 ? "\u26A0" : prio === "P1" ? "\u25B2" : "\u25CF";

  card.innerHTML = `
    <div class="uq-header">
      <span class="uq-from">${escapeHtml(from)} ${prioIcon}</span>
      <span class="uq-type">${escapeHtml(prio)} TASK</span>
    </div>
    <button class="uq-dismiss">&times;</button>
    <div class="uq-subject">${escapeHtml(task.title || "(untitled)")}</div>
    ${task.description ? `<div class="uq-content">${escapeHtml(task.description)}</div>` : ""}
    <div style="display:flex;gap:6px;margin-top:10px;">
      <button class="uq-respond-btn" data-action="accept">Accept</button>
      <button class="uq-respond-btn" data-action="done" style="background:#00e676;color:#0a0a12;">Complete</button>
    </div>
  `;

  card.querySelector(".uq-dismiss").addEventListener("click", () => {
    card.style.opacity = "0";
    card.style.transition = "opacity 0.3s ease";
    setTimeout(() => card.remove(), 300);
  });

  card.querySelector('[data-action="accept"]').addEventListener("click", async (e) => {
    const btn = e.target;
    btn.disabled = true;
    btn.textContent = "...";
    const result = await client.transitionTask(task.id, "in-progress", task.project || "default", "user");
    if (result) {
      btn.textContent = "Working";
      btn.style.background = "rgba(0,230,118,0.2)";
      btn.style.color = "#00e676";
    } else {
      btn.disabled = false;
      btn.textContent = "Accept";
    }
  });

  card.querySelector('[data-action="done"]').addEventListener("click", async (e) => {
    const btn = e.target;
    btn.disabled = true;
    btn.textContent = "...";
    const result = await client.transitionTask(task.id, "done", task.project || "default", "user");
    if (result) {
      card.style.opacity = "0";
      card.style.transition = "opacity 0.3s ease";
      setTimeout(() => card.remove(), 300);
    } else {
      btn.disabled = false;
      btn.textContent = "Done";
    }
  });

  userQuestionsPanel.appendChild(card);
}

// --- Agent detail panel ---

function openDetail(av) {
  focusedAgent = agentKey(av.project, av.name);
  focusedTeam = null;
  detailPanel.classList.add("open");
  detailName.textContent = av.name;
  detailName.style.color = av.color;
  detailRole.textContent = av.role || "\u2014";
  detailDesc.textContent = av.description || "\u2014";
  detailProject.textContent = av.project !== "default" ? `Project: ${av.project}` : "";
  detailStatus.textContent = av.online ? "Online" : "Offline";
  detailStatus.style.color = av.online ? "#00e676" : "#636e72";
  detailLastSeen.textContent = formatTime(av._lastSeenRaw);
  detailRegistered.textContent = formatTime(av._registeredRaw);

  // Reports To
  if (av._reportsTo) {
    detailReportsTo.innerHTML = "";
    const link = document.createElement("span");
    link.className = "detail-hierarchy-link";
    link.textContent = av._reportsTo;
    link.addEventListener("click", () => {
      const managerKey = agentKey(av.project, av._reportsTo);
      const managerAv = agentViews.get(managerKey);
      if (managerAv) openDetail(managerAv);
    });
    detailReportsTo.appendChild(link);
  } else {
    detailReportsTo.textContent = "\u2014";
  }

  // Direct Reports
  const directReports = [];
  for (const a of agentsData) {
    const aProject = a.project || "default";
    if (a.reports_to === av.name && aProject === av.project) {
      directReports.push(a.name);
    }
  }

  if (directReports.length > 0) {
    detailDirectReports.innerHTML = "";
    const container = document.createElement("div");
    container.className = "detail-reports-list";
    for (const name of directReports) {
      const tag = document.createElement("span");
      tag.className = "detail-report-tag";
      tag.textContent = name;
      tag.addEventListener("click", () => {
        const reportKey = agentKey(av.project, name);
        const reportAv = agentViews.get(reportKey);
        if (reportAv) openDetail(reportAv);
      });
      container.appendChild(tag);
    }
    detailDirectReports.appendChild(container);
  } else {
    detailDirectReports.textContent = "\u2014";
  }

  // Teams
  const detailTeamsEl = document.getElementById("detail-teams");
  if (detailTeamsEl) {
    const teams = av._teams || [];
    if (teams.length > 0) {
      detailTeamsEl.innerHTML = "";
      const container = document.createElement("div");
      container.className = "detail-reports-list";
      for (const t of teams) {
        const tag = document.createElement("span");
        tag.className = `detail-team-tag detail-team-type-${t.type}`;
        tag.textContent = t.type === "admin" ? `★ ${t.name}` : t.name;
        tag.title = `Role: ${t.role} | Type: ${t.type}`;
        container.appendChild(tag);
      }
      detailTeamsEl.appendChild(container);
    } else {
      detailTeamsEl.textContent = "\u2014";
    }
  }

  // Show tasks for this agent
  const detailTasksEl = document.getElementById("detail-tasks");
  if (detailTasksEl) {
    const agentTasks = allTasks.filter(t => {
      const tp = t.project || "default";
      return tp === av.project && (t.assigned_to === av.name || t.dispatched_by === av.name);
    }).filter(t => t.status !== "done").slice(0, 5);

    if (agentTasks.length > 0) {
      detailTasksEl.innerHTML = agentTasks.map(t => {
        const statusColors = { pending: "#ffd93d", accepted: "#74b9ff", "in-progress": "#00e676", blocked: "#ff6b6b" };
        const color = statusColors[t.status] || "#636e72";
        return `<div class="detail-task-item">
          <span style="color:${color}">[${t.status}]</span>
          <span>${escapeHtml(t.title.length > 30 ? t.title.slice(0, 28) + "..." : t.title)}</span>
          <span style="color:#636e72">${t.priority}</span>
        </div>`;
      }).join("");
    } else {
      detailTasksEl.textContent = "\u2014";
    }
  }

  // Filter messages to this agent
  loadMessages();
}

detailClose.addEventListener("click", () => {
  detailPanel.classList.remove("open");
  focusedAgent = null;
  loadMessages();
});

// --- Pan/Zoom input handlers ---

let dragging = false;
let dragStartX = 0;
let dragStartY = 0;
let dragMoved = false;

canvas.addEventListener("mousedown", (e) => {
  const rect = canvas.getBoundingClientRect();
  const sx = e.clientX - rect.left;
  const sy = e.clientY - rect.top;

  // Check if clicking on an agent (don't start pan)
  const wp = engine.camera.screenToWorld(sx, sy, engine.width, engine.height);
  for (const [, av] of agentViews) {
    if (av.hitTest(wp.x, wp.y)) {
      return; // Let the click handler deal with it
    }
  }

  dragging = true;
  dragMoved = false;
  dragStartX = e.clientX;
  dragStartY = e.clientY;
});

canvas.addEventListener("mousemove", (e) => {
  if (dragging) {
    const dx = e.clientX - dragStartX;
    const dy = e.clientY - dragStartY;
    if (Math.abs(dx) > 2 || Math.abs(dy) > 2) {
      dragMoved = true;
    }
    engine.camera.pan(dx, dy);
    dragStartX = e.clientX;
    dragStartY = e.clientY;
    canvas.style.cursor = "grabbing";
    return;
  }

  // Hover cursor + agent hover state
  const rect = canvas.getBoundingClientRect();
  const sx = e.clientX - rect.left;
  const sy = e.clientY - rect.top;
  const wp = engine.camera.screenToWorld(sx, sy, engine.width, engine.height);

  let newHovered = null;
  for (const [key, av] of agentViews) {
    if (av.hitTest(wp.x, wp.y)) {
      newHovered = key;
      break;
    }
  }

  if (newHovered !== hoveredAgentKey) {
    if (hoveredAgentKey) {
      const prev = agentViews.get(hoveredAgentKey);
      if (prev) prev.hovered = false;
    }
    if (newHovered) {
      const next = agentViews.get(newHovered);
      if (next) next.hovered = true;
    }
    hoveredAgentKey = newHovered;
  }

  canvas.style.cursor = newHovered ? "pointer" : "default";
});

canvas.addEventListener("mouseup", () => {
  dragging = false;
  canvas.style.cursor = "default";
});

canvas.addEventListener("mouseleave", () => {
  dragging = false;
  if (hoveredAgentKey) {
    const prev = agentViews.get(hoveredAgentKey);
    if (prev) prev.hovered = false;
    hoveredAgentKey = null;
  }
});

// Click handler (uses world coords)
canvas.addEventListener("click", (e) => {
  if (dragMoved) return; // Was a pan drag, not a click

  const rect = canvas.getBoundingClientRect();
  const sx = e.clientX - rect.left;
  const sy = e.clientY - rect.top;
  const wp = engine.camera.screenToWorld(sx, sy, engine.width, engine.height);

  // 1. Check agent hit
  for (const [, av] of agentViews) {
    if (av.hitTest(wp.x, wp.y)) {
      av.triggerRipple();
      openDetail(av);
      return;
    }
  }

  // 2. Check team group hit (single-project mode: click near a team cluster)
  if (projectGroups.size <= 1) {
    const project = ([...projectGroups.keys()][0]) || "default";
    // Collect unique team slugs
    const seenTeams = new Set();
    for (const a of agentsData) {
      if ((a.project || "default") !== project || !a.teams) continue;
      for (const t of a.teams) {
        if (t.type === "admin" || seenTeams.has(t.slug)) continue;
        seenTeams.add(t.slug);
        const memberKeys = getTeamMemberKeys(project, t.slug);
        if (memberKeys.length < 2) continue;
        // Compute team center from member positions
        let tcx = 0, tcy = 0, count = 0;
        for (const mk of memberKeys) {
          const mav = agentViews.get(mk);
          if (mav) { tcx += mav.x; tcy += mav.y; count++; }
        }
        if (count === 0) continue;
        tcx /= count; tcy /= count;
        // Compute bounding radius
        let maxR = 0;
        for (const mk of memberKeys) {
          const mav = agentViews.get(mk);
          if (mav) {
            const d = Math.sqrt((mav.x - tcx) ** 2 + (mav.y - tcy) ** 2);
            if (d > maxR) maxR = d;
          }
        }
        const hitR = maxR + 60;
        const tdx = wp.x - tcx;
        const tdy = wp.y - tcy;
        if (tdx * tdx + tdy * tdy <= hitR * hitR) {
          const memberNames = memberKeys.map(k => k.split(":")[1]);
          const alreadyFocused = focusedTeam && focusedTeam.slug === t.slug && focusedTeam.project === project;
          if (!alreadyFocused) {
            focusedTeam = { project, slug: t.slug, members: memberNames };
            focusedAgent = null;
            focusedProject = project;
            detailPanel.classList.remove("open");
            loadMessages();
            if (activeTab === "tasks") renderTasks();
            if (currentMode === "kanban") kanbanBoard.setTasks(getViewFilteredTasks());
            return;
          }
          return;
        }
      }
    }
  }

  // 3. Check cluster hit — click inside a cluster circle → zoom to that project
  for (const cluster of world.clusters) {
    const dx = wp.x - cluster.cx;
    const dy = wp.y - cluster.cy;
    if (dx * dx + dy * dy <= cluster.radius * cluster.radius) {
      if (focusedProject !== cluster.project) {
        focusedProject = cluster.project;
        focusedAgent = null;
        focusedTeam = null;
        detailPanel.classList.remove("open");
        fitToCluster(cluster);
        loadMessages();
        return;
      }
      // Already focused on this cluster — do nothing
      return;
    }
  }

  // 4. Click on empty space → zoom back to show all, clear focus
  detailPanel.classList.remove("open");
  focusedAgent = null;
  focusedTeam = null;
  if (focusedProject) {
    focusedProject = null;
    fitToAllClusters();
  }
  loadMessages();
});

// Zoom with wheel
canvas.addEventListener("wheel", (e) => {
  e.preventDefault();
  const rect = canvas.getBoundingClientRect();
  const sx = e.clientX - rect.left;
  const sy = e.clientY - rect.top;
  engine.camera.zoomAt(sx, sy, e.deltaY, engine.width, engine.height);
}, { passive: false });

// Re-layout + re-fit on resize
window.addEventListener("resize", () => {
  layoutAgents();
});

// --- Helpers ---

function formatTime(isoStr) {
  if (!isoStr) return "\u2014";
  try {
    const d = new Date(isoStr);
    return d.toLocaleTimeString("en", {
      hour12: false,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return isoStr;
  }
}

function escapeHtml(str) {
  return str
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// --- Memory panel ---

const tabMessages = document.getElementById("tab-messages");
const tabMemories = document.getElementById("tab-memories");
const messagesPanel = document.getElementById("messages-panel");
const memoriesPanel = document.getElementById("memories-panel");
const memoriesList = document.getElementById("memories-list");
const memoriesSearch = document.getElementById("memories-search");
const memoriesScopeFilter = document.getElementById("memories-scope-filter");
const memoriesProjectFilter = document.getElementById("memories-project-filter");
const memoryCountEl = document.getElementById("memory-count");

const tabTasks = document.getElementById("tab-tasks");
const tasksPanel = document.getElementById("tasks-panel");
const tasksList = document.getElementById("tasks-list");
const tasksStatusFilter = document.getElementById("tasks-status-filter");
const tasksPriorityFilter = document.getElementById("tasks-priority-filter");
const tasksMineFilter = document.getElementById("tasks-mine-filter");
const taskCountEl = document.getElementById("task-count");
let showMyTasksOnly = false;

let activeTab = "messages";

tabMessages.addEventListener("click", () => {
  activeTab = "messages";
  tabMessages.classList.add("active");
  tabMemories.classList.remove("active");
  tabTasks.classList.remove("active");
  messagesPanel.classList.remove("hidden");
  memoriesPanel.classList.add("hidden");
  tasksPanel.classList.add("hidden");
});

tabMemories.addEventListener("click", () => {
  activeTab = "memories";
  tabMemories.classList.add("active");
  tabMessages.classList.remove("active");
  tabTasks.classList.remove("active");
  memoriesPanel.classList.remove("hidden");
  messagesPanel.classList.add("hidden");
  tasksPanel.classList.add("hidden");
  loadMemories();
});

let searchTimeout = null;
memoriesSearch.addEventListener("input", () => {
  clearTimeout(searchTimeout);
  searchTimeout = setTimeout(() => loadMemories(), 300);
});
memoriesScopeFilter.addEventListener("change", () => loadMemories());
memoriesProjectFilter.addEventListener("change", () => loadMemories());

tabTasks.addEventListener("click", () => {
  activeTab = "tasks";
  tabTasks.classList.add("active");
  tabMessages.classList.remove("active");
  tabMemories.classList.remove("active");
  tasksPanel.classList.remove("hidden");
  messagesPanel.classList.add("hidden");
  memoriesPanel.classList.add("hidden");
  loadTasks();
});

tasksStatusFilter.addEventListener("change", () => renderTasks());
tasksPriorityFilter.addEventListener("change", () => renderTasks());
tasksMineFilter.addEventListener("click", () => {
  showMyTasksOnly = !showMyTasksOnly;
  tasksMineFilter.classList.toggle("active", showMyTasksOnly);
  renderTasks();
});

async function loadMemories() {
  const query = memoriesSearch.value.trim();
  const scope = memoriesScopeFilter.value;
  const project = memoriesProjectFilter.value;

  let memories;
  if (query) {
    memories = await client.searchMemories(query);
    if (scope) memories = memories.filter(m => m.scope === scope);
    if (project) memories = memories.filter(m => m.project === project);
  } else {
    memories = await client.fetchMemories({ scope, project });
  }

  memoryCountEl.textContent = memories.length;
  renderMemories(memories);
}

function renderMemories(memories) {
  memoriesList.innerHTML = "";

  if (memories.length === 0) {
    memoriesList.innerHTML = '<div class="msg-empty">No memories yet</div>';
    return;
  }

  // Update project filter options from data
  const projects = new Set(memories.map(m => m.project));
  const currentVal = memoriesProjectFilter.value;
  memoriesProjectFilter.innerHTML = '<option value="">All projects</option>';
  for (const p of [...projects].sort()) {
    const opt = document.createElement("option");
    opt.value = p;
    opt.textContent = p;
    if (p === currentVal) opt.selected = true;
    memoriesProjectFilter.appendChild(opt);
  }

  for (const mem of memories) {
    const el = document.createElement("div");
    el.className = "memory-item" + (mem.conflict_with ? " memory-conflict" : "");

    const tags = parseTags(mem.tags);
    const tagsHtml = tags.map(t => `<span class="memory-tag">${escapeHtml(t)}</span>`).join("");

    const val = mem.value.length > 200 ? mem.value.slice(0, 200) + "..." : mem.value;
    const time = formatTime(mem.updated_at);

    el.innerHTML = `
      <div class="memory-header">
        <span class="memory-key">${escapeHtml(mem.key)}</span>
        <span class="memory-scope memory-scope-${mem.scope}">${mem.scope}</span>
        ${mem.conflict_with ? '<span class="memory-conflict-badge">CONFLICT</span>' : ""}
      </div>
      <div class="memory-value">${escapeHtml(val)}</div>
      <div class="memory-meta">
        <span class="memory-agent">${escapeHtml(mem.agent_name)}</span>
        <span class="memory-confidence">${mem.confidence}</span>
        <span class="memory-version">v${mem.version}</span>
        ${tagsHtml}
        <span class="memory-time">${time}</span>
      </div>
      <div class="memory-actions">
        <button class="memory-delete-btn" title="Archive">&#x2715;</button>
      </div>
    `;

    el.querySelector(".memory-delete-btn").addEventListener("click", async (e) => {
      e.stopPropagation();
      const ok = await client.deleteMemory(mem.id);
      if (ok) {
        el.style.opacity = "0";
        setTimeout(() => { el.remove(); loadMemories(); }, 200);
      }
    });

    el.addEventListener("click", () => {
      const existing = el.querySelector(".memory-expanded");
      if (existing) {
        existing.remove();
        return;
      }
      const expanded = document.createElement("div");
      expanded.className = "memory-expanded";
      expanded.textContent = mem.value;
      el.appendChild(expanded);
    });

    memoriesList.appendChild(el);
  }
}

function parseTags(tagsStr) {
  try {
    const parsed = JSON.parse(tagsStr);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

// Poll memories every 15s when tab is active
setInterval(() => {
  if (activeTab === "memories") loadMemories();
}, 15000);

// --- Tasks panel ---

let allTasks = [];

function onNewTasks(tasks) {
  for (const task of tasks) {
    const idx = allTasks.findIndex(t => t.id === task.id);
    if (idx >= 0) {
      allTasks[idx] = task;
    } else {
      allTasks.push(task);
    }
  }
  taskCountEl.textContent = allTasks.filter(t => t.status !== "done").length;
  if (activeTab === "tasks") renderTasks();
  updateAgentTaskLabels();

  // Canvas effects for new task completions (only fire once per task)
  for (const task of tasks) {
    if (task.status === "done" && task.assigned_to && !_celebratedTasks.has(task.id)) {
      _celebratedTasks.add(task.id);
      const key = agentKey(task.project || "default", task.assigned_to);
      const av = agentViews.get(key);
      if (av) av.particles.emit("celebrate", av.x, av.y - 10);
    }
  }

  // Notify user of tasks dispatched to them
  for (const task of tasks) {
    const isForUser = (task.profile_slug === "user" || task.profile_slug === "founder")
      && task.status === "pending" && !shownUserMsgs.has("task:" + task.id);
    if (isForUser) {
      shownUserMsgs.add("task:" + task.id);
      showUserTaskCard(task);
    }
  }

  // Update kanban if visible
  if (currentMode === "kanban") {
    kanbanBoard.setTasks(getViewFilteredTasks());
  }
}

async function loadTasks() {
  const tasks = await client.fetchAllTasks();
  allTasks = tasks;
  taskCountEl.textContent = allTasks.filter(t => t.status !== "done").length;
  renderTasks();
}

/** Filter tasks by current view context (global > project > team > agent). */
function getViewFilteredTasks() {
  if (focusedAgent) {
    const av = agentViews.get(focusedAgent);
    if (!av) return [];
    return allTasks.filter(t => {
      const tp = t.project || "default";
      return tp === av.project && (t.assigned_to === av.name || t.dispatched_by === av.name || t.profile_slug === av.name);
    });
  } else if (focusedTeam) {
    const memberSet = new Set(focusedTeam.members);
    return allTasks.filter(t => {
      const tp = t.project || "default";
      return tp === focusedTeam.project && (memberSet.has(t.assigned_to) || memberSet.has(t.dispatched_by));
    });
  } else if (focusedProject) {
    return allTasks.filter(t => (t.project || "default") === focusedProject);
  }
  return allTasks;
}

function renderTasks() {
  const status = tasksStatusFilter.value;
  const priority = tasksPriorityFilter.value;

  let filtered = getViewFilteredTasks();
  if (showMyTasksOnly) filtered = filtered.filter(t => (t.profile_slug === "user" || t.profile_slug === "founder") && t.status !== "done");
  if (status) filtered = filtered.filter(t => t.status === status);
  if (priority) filtered = filtered.filter(t => t.priority === priority);

  if (filtered.length === 0) {
    tasksList.innerHTML = '<div class="msg-empty">No tasks</div>';
    return;
  }

  // Build set of task IDs we want to show
  const wantedIds = new Set(filtered.map(t => t.id));

  // Remove items no longer in the filtered list
  tasksList.querySelectorAll(".task-item[data-task-id]").forEach(el => {
    if (!wantedIds.has(el.dataset.taskId)) el.remove();
  });

  // Build map of existing DOM items
  const existingEls = new Map();
  tasksList.querySelectorAll(".task-item[data-task-id]").forEach(el => {
    existingEls.set(el.dataset.taskId, el);
  });

  // Remove empty placeholder if present
  const empty = tasksList.querySelector(".msg-empty");
  if (empty) empty.remove();

  for (const task of filtered) {
    const existing = existingEls.get(task.id);
    if (existing) {
      // Update in-place: status, priority, title
      const statusEl = existing.querySelector(".task-status");
      const prioEl = existing.querySelector(".task-priority");
      const titleEl = existing.querySelector(".task-title");
      if (statusEl && statusEl.textContent !== task.status) {
        statusEl.textContent = task.status;
        statusEl.className = `task-status task-status-${task.status}`;
      }
      if (prioEl && prioEl.textContent !== task.priority) {
        prioEl.textContent = task.priority;
        prioEl.className = `task-priority task-priority-${task.priority}`;
      }
      if (titleEl) titleEl.textContent = task.title;
      continue;
    }

    const el = document.createElement("div");
    const isMine = task.profile_slug === "user" || task.profile_slug === "founder";
    el.className = "task-item" + (isMine ? " task-mine" : "");
    el.dataset.taskId = task.id;

    const statusClass = `task-status-${task.status}`;
    const priorityClass = `task-priority-${task.priority}`;
    const time = formatTime(task.dispatched_at);
    const desc = task.description ? (task.description.length > 100 ? task.description.slice(0, 100) + "..." : task.description) : "";

    let extraHtml = "";
    if (task.status === "done" && task.result) {
      const r = task.result.length > 150 ? task.result.slice(0, 150) + "..." : task.result;
      extraHtml = `<div class="task-result">${escapeHtml(r)}</div>`;
    }
    if (task.status === "blocked" && task.blocked_reason) {
      extraHtml = `<div class="task-blocked-reason">${escapeHtml(task.blocked_reason)}</div>`;
    }

    el.innerHTML = `
      <div class="task-header">
        <span class="task-priority ${priorityClass}">${task.priority}</span>
        <span class="task-title">${escapeHtml(task.title)}</span>
        <span class="task-status ${statusClass}">${task.status}</span>
      </div>
      ${desc ? `<div class="task-description">${escapeHtml(desc)}</div>` : ""}
      ${extraHtml}
      <div class="task-meta">
        <span class="task-profile">${escapeHtml(task.profile_slug)}</span>
        ${task.assigned_to ? `<span class="task-agent">${escapeHtml(task.assigned_to)}</span>` : ""}
        <span class="task-time">${time}</span>
      </div>
    `;

    el.addEventListener("click", () => {
      const existing = el.querySelector(".task-expanded");
      if (existing) { existing.remove(); return; }
      const expanded = document.createElement("div");
      expanded.className = "task-expanded";
      let details = `ID: ${task.id}\nProfile: ${task.profile_slug}\nDispatched by: ${task.dispatched_by}\nPriority: ${task.priority}\nStatus: ${task.status}`;
      if (task.assigned_to) details += `\nAssigned to: ${task.assigned_to}`;
      if (task.description) details += `\n\nDescription:\n${task.description}`;
      if (task.result) details += `\n\nResult:\n${task.result}`;
      if (task.blocked_reason) details += `\n\nBlocked: ${task.blocked_reason}`;
      expanded.textContent = details;
      el.appendChild(expanded);
    });

    tasksList.appendChild(el);
  }
}

// Poll tasks every 5s when tab is active
setInterval(() => {
  if (activeTab === "tasks") loadTasks();
}, 5000);

// --- Font scale ---

const savedScale = localStorage.getItem("font-scale") || "1.2";
document.body.style.setProperty("--scale", savedScale);

document.querySelectorAll(".scale-btn").forEach(btn => {
  if (btn.dataset.scale === savedScale) btn.classList.add("active");
  else btn.classList.remove("active");

  btn.addEventListener("click", () => {
    const scale = btn.dataset.scale;
    document.body.style.setProperty("--scale", scale);
    localStorage.setItem("font-scale", scale);
    document.querySelectorAll(".scale-btn").forEach(b => b.classList.remove("active"));
    btn.classList.add("active");
  });
});

// --- Agent task label integration ---

function updateAgentTaskLabels() {
  // Clear all task labels first
  for (const [, av] of agentViews) {
    av.currentTaskLabel = null;
    av.isBlocked = false;
  }

  for (const task of allTasks) {
    if (!task.assigned_to) continue;
    const taskProject = task.project || "default";
    const key = agentKey(taskProject, task.assigned_to);
    const av = agentViews.get(key);
    if (!av) continue;

    if (task.status === "in-progress") {
      av.currentTaskLabel = task.title;
    } else if (task.status === "blocked") {
      av.currentTaskLabel = task.title;
      av.isBlocked = true;
    }
  }
}

// --- Connection overlay (teams + hierarchy) ---

function updateConnectionOverlay() {
  // Build teams with memberKeys from agentsData
  const teamMap = new Map(); // slug -> {slug, name, type, memberKeys: Set}

  for (const a of agentsData) {
    const project = a.project || "default";
    if (!a.teams) continue;
    for (const t of a.teams) {
      const teamKey = `${project}:${t.slug}`;
      if (!teamMap.has(teamKey)) {
        teamMap.set(teamKey, {
          slug: t.slug,
          name: t.name,
          type: t.type,
          memberKeys: [],
        });
      }
      teamMap.get(teamKey).memberKeys.push(agentKey(project, a.name));
    }
  }

  connectionOverlay.setData(agentViews, [...teamMap.values()]);
}

/** Get agent keys for all members of a team in a given project */
function getTeamMemberKeys(project, teamSlug) {
  const keys = [];
  for (const a of agentsData) {
    const aProject = a.project || "default";
    if (aProject !== project) continue;
    if (a.teams && a.teams.some(t => t.slug === teamSlug)) {
      keys.push(agentKey(project, a.name));
    }
  }
  return keys;
}

// Fetch teams periodically for the overlay
let _teamsFetchTimer = null;
async function fetchTeamsData() {
  teamsData = await client.fetchAllTeams();
}

// --- Message search & conversation filter ---

const msgSearchInput = document.getElementById("msg-search");
const msgConvFilter = document.getElementById("msg-conv-filter");

if (msgSearchInput) {
  let msgSearchTimeout = null;
  msgSearchInput.addEventListener("input", () => {
    clearTimeout(msgSearchTimeout);
    msgSearchTimeout = setTimeout(() => filterMessages(), 300);
  });
}
if (msgConvFilter) {
  msgConvFilter.addEventListener("change", () => {
    filterMessages();
    updateHighlights();
  });
}

function filterMessages() {
  const query = msgSearchInput ? msgSearchInput.value.trim().toLowerCase() : "";
  const convId = msgConvFilter ? msgConvFilter.value : "";
  const items = messagesList.querySelectorAll(".msg-item");
  for (const item of items) {
    const text = item.textContent.toLowerCase();
    const itemConv = item.dataset.convId || "";
    const matchSearch = !query || text.includes(query);
    const matchConv = !convId || itemConv === convId;
    item.style.display = (matchSearch && matchConv) ? "" : "none";
  }
}

// Update conversation filter options
function updateConvFilterOptions() {
  if (!msgConvFilter) return;
  const currentVal = msgConvFilter.value;
  msgConvFilter.innerHTML = '<option value="">All conversations</option>';
  for (const conv of conversations) {
    const opt = document.createElement("option");
    opt.value = conv.id;
    opt.textContent = conv.title || conv.id.slice(0, 8);
    if (conv.id === currentVal) opt.selected = true;
    msgConvFilter.appendChild(opt);
  }
}

// --- Layout modes ---

let currentMode = "canvas"; // "canvas" | "detail" | "kanban"

function setMode(mode) {
  currentMode = mode;
  const main = document.getElementById("main");
  main.classList.remove("mode-canvas", "mode-detail", "mode-kanban");
  main.classList.add(`mode-${mode}`);

  // Update header mode buttons
  document.querySelectorAll(".mode-btn").forEach(btn => {
    btn.classList.toggle("active", btn.dataset.mode === mode);
  });

  // Show/hide kanban
  if (mode === "kanban") {
    kanbanBoard.show();
    // Fetch all tasks + boards, then filter by current view context
    Promise.all([client.fetchAllTasks(), client.fetchAllBoards()]).then(([tasks, boards]) => {
      allTasks = tasks;
      kanbanBoard.setBoards(boards);
      kanbanBoard.setTasks(getViewFilteredTasks());
      taskCountEl.textContent = allTasks.filter(t => t.status !== "done").length;
    });
  } else {
    kanbanBoard.hide();
  }

  // Messages panel: hidden in kanban mode
  if (mode === "kanban") {
    messagesPanel.classList.add("hidden");
    memoriesPanel.classList.add("hidden");
    tasksPanel.classList.add("hidden");
  } else if (activeTab === "messages") {
    messagesPanel.classList.remove("hidden");
  } else if (activeTab === "memories") {
    memoriesPanel.classList.remove("hidden");
  } else if (activeTab === "tasks") {
    tasksPanel.classList.remove("hidden");
  }
}

// Wire mode buttons
document.querySelectorAll(".mode-btn").forEach(btn => {
  btn.addEventListener("click", () => setMode(btn.dataset.mode));
});

// --- Typewriter effect for new messages ---

function typewriterAppend(el, text, speed = 12) {
  const contentEl = el.querySelector(".msg-content");
  if (!contentEl) return;
  contentEl.textContent = "";
  contentEl.classList.add("typing");
  let i = 0;
  const interval = setInterval(() => {
    if (i < text.length) {
      contentEl.textContent += text[i];
      i++;
    } else {
      clearInterval(interval);
      contentEl.classList.remove("typing");
    }
  }, speed);
}

// --- Kanban board ---

const kanbanPanel = document.getElementById("kanban-panel");
const kanbanBoard = new KanbanBoard(kanbanPanel);
window._kanbanBoard = kanbanBoard;
kanbanBoard.hide();

kanbanBoard.onTransition = async (taskId, newStatus, agentName) => {
  const task = allTasks.find(t => t.id === taskId);
  const project = task ? task.project || "default" : "default";
  const result = await client.transitionTask(taskId, newStatus, project, agentName || "user");
  if (result) {
    // Update local state
    const idx = allTasks.findIndex(t => t.id === taskId);
    if (idx >= 0) allTasks[idx] = result;
    kanbanBoard.setTasks(getViewFilteredTasks());
    updateAgentTaskLabels();
    if (activeTab === "tasks") renderTasks();
    taskCountEl.textContent = allTasks.filter(t => t.status !== "done").length;
  }
};

kanbanBoard.onDispatch = async (data) => {
  const project = focusedProject || "default";
  const result = await client.dispatchTask({
    project,
    profile: data.profile,
    title: data.title,
    description: data.description,
    priority: data.priority,
    parent_task_id: data.parent_task_id || undefined,
  });
  if (result) {
    allTasks.push(result);
    kanbanBoard.setTasks(getViewFilteredTasks());
    taskCountEl.textContent = allTasks.filter(t => t.status !== "done").length;
  }
};

kanbanBoard.onDelete = async (taskId, project) => {
  const ok = await client.deleteTask(taskId, project);
  if (ok) {
    allTasks = allTasks.filter(t => t.id !== taskId);
    kanbanBoard.setTasks(getViewFilteredTasks());
    taskCountEl.textContent = allTasks.filter(t => t.status !== "done").length;
    if (activeTab === "tasks") renderTasks();
  }
};

kanbanBoard.onEdit = async (taskId, project, data) => {
  const result = await client.updateTask(taskId, { project, ...data });
  if (result) {
    const idx = allTasks.findIndex(t => t.id === taskId);
    if (idx >= 0) allTasks[idx] = result;
    kanbanBoard.setTasks(getViewFilteredTasks());
    if (activeTab === "tasks") renderTasks();
  }
};

// --- Keyboard shortcuts ---

const shortcuts = new ShortcutManager();

shortcuts.register("1", "mode-canvas", "Canvas mode", () => setMode("canvas"));
shortcuts.register("3", "mode-kanban", "Kanban mode", () => setMode("kanban"));
shortcuts.register("Escape", "close", "Close / return to default", () => {
  if (detailPanel.classList.contains("open")) {
    detailPanel.classList.remove("open");
    focusedAgent = null;
    loadMessages();
  } else if (currentMode !== "canvas") {
    setMode("canvas");
  }
});
shortcuts.register("/", "search", "Focus search", () => {
  if (currentMode === "kanban") return;
  if (msgSearchInput) msgSearchInput.focus();
});
shortcuts.register("n", "new-task", "New task", () => {
  if (currentMode !== "kanban") setMode("kanban");
  kanbanBoard._showDispatchForm();
});

// Agent navigation with arrows
let navIndex = -1;
function getAgentKeys() { return [...agentViews.keys()].sort(); }

shortcuts.register("ArrowRight", "nav-next", "Next agent", () => {
  const keys = getAgentKeys();
  if (keys.length === 0) return;
  navIndex = (navIndex + 1) % keys.length;
  const av = agentViews.get(keys[navIndex]);
  if (av) { av.triggerRipple(); openDetail(av); }
});
shortcuts.register("ArrowLeft", "nav-prev", "Previous agent", () => {
  const keys = getAgentKeys();
  if (keys.length === 0) return;
  navIndex = (navIndex - 1 + keys.length) % keys.length;
  const av = agentViews.get(keys[navIndex]);
  if (av) { av.triggerRipple(); openDetail(av); }
});

// Font scale shortcuts
const scaleValues = ["1", "1.2", "1.5"];
shortcuts.register("+", "scale-up", "Font scale up", () => {
  const cur = document.body.style.getPropertyValue("--scale") || "1.2";
  const idx = scaleValues.indexOf(cur);
  if (idx < scaleValues.length - 1) {
    const next = scaleValues[idx + 1];
    document.body.style.setProperty("--scale", next);
    localStorage.setItem("font-scale", next);
    document.querySelectorAll(".scale-btn").forEach(b => b.classList.toggle("active", b.dataset.scale === next));
  }
});
shortcuts.register("=", "scale-up", "Font scale up", () => {
  const cur = document.body.style.getPropertyValue("--scale") || "1.2";
  const idx = scaleValues.indexOf(cur);
  if (idx < scaleValues.length - 1) {
    const next = scaleValues[idx + 1];
    document.body.style.setProperty("--scale", next);
    localStorage.setItem("font-scale", next);
    document.querySelectorAll(".scale-btn").forEach(b => b.classList.toggle("active", b.dataset.scale === next));
  }
});
shortcuts.register("-", "scale-down", "Font scale down", () => {
  const cur = document.body.style.getPropertyValue("--scale") || "1.2";
  const idx = scaleValues.indexOf(cur);
  if (idx > 0) {
    const prev = scaleValues[idx - 1];
    document.body.style.setProperty("--scale", prev);
    localStorage.setItem("font-scale", prev);
    document.querySelectorAll(".scale-btn").forEach(b => b.classList.toggle("active", b.dataset.scale === prev));
  }
});

shortcuts.register("?", "help", "Toggle help", () => toggleHelp());

shortcuts.start();

// --- Start ---

console.log("[relay] UI initializing...");
const client = new APIClient(onAgents, onConversations, onNewMessages, onNewTasks, onActivity);
client.start();
loadMessages();
loadTasks();
fetchTeamsData();
_teamsFetchTimer = setInterval(fetchTeamsData, 10000);
console.log("[relay] polling started");

// --- Help modal ---
const helpBtn = document.getElementById("help-btn");
const helpModal = document.getElementById("help-modal");
const helpClose = document.getElementById("help-close");
const helpOverlay = helpModal ? helpModal.querySelector(".help-overlay") : null;

function toggleHelp() {
  if (helpModal) helpModal.classList.toggle("hidden");
}
if (helpBtn) helpBtn.addEventListener("click", toggleHelp);
if (helpClose) helpClose.addEventListener("click", toggleHelp);
if (helpOverlay) helpOverlay.addEventListener("click", toggleHelp);
