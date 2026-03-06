import { SpriteGenerator, PALETTE_COLORS, ACTIVITY_GLOW, ACTIVITY_FRAME_SPEED } from "./sprite.js";
import { Bubble } from "./bubble.js";
import { ParticleEmitter } from "./particles.js";

// Message type → color for arrival bursts
const ARRIVAL_COLORS = {
  question:     "#ffd740",
  response:     "#55efc4",
  notification: "#a29bfe",
  task:         "#ff7675",
  default:      "#74b9ff",
};

const SPRITE_SIZE = 64; // 32px * 2 scale
const HALF = SPRITE_SIZE / 2;

export class AgentView {
  constructor(name, role, description, paletteIndex, online, project) {
    this.name = name;
    this.role = role;
    this.description = description;
    this.paletteIndex = paletteIndex;
    this.online = online;
    this.project = project || "default";
    this.isExecutive = false;
    this.currentTaskLabel = null;
    this.isBlocked = false;
    this._teams = [];

    this.x = 0;
    this.y = 0;
    this.targetX = 0;
    this.targetY = 0;

    this.spawnAlpha = 0;
    this.spawning = true;
    this.frameIndex = 0;
    this.frameTimer = 0;
    this.highlighted = false;

    // New sprite system: archetype + golden
    const spriteData = SpriteGenerator.generate(paletteIndex, name);
    this.frames = spriteData.frames;
    this.archetype = spriteData.archetype;
    this.isGolden = spriteData.isGolden;
    this.color = PALETTE_COLORS[spriteData.paletteIndex % PALETTE_COLORS.length];

    this.bubble = null;
    this.particles = new ParticleEmitter();

    // Presence & aura
    this.glowPhase = Math.random() * Math.PI * 2;
    this.breathPhase = Math.random() * Math.PI * 2;
    this.hovered = false;
    this.ripples = [];
    this._blockedPhase = 0;
    this._workingPhase = 0;
    this._shakeX = 0;
    this.sleeping = false;
    this._sleepPhase = 0;
  }

  setPosition(x, y) {
    this.x = x;
    this.y = y;
    this.targetX = x;
    this.targetY = y;
  }

  showBubble(text, type = "speech") {
    this.bubble = new Bubble(text, type);
  }

  hitTest(px, py) {
    return Math.abs(px - this.x) < HALF && Math.abs(py - this.y) < HALF;
  }

  spawnEffect() {
    this.particles.emit("spawn", this.x, this.y + 24);
  }

  triggerRipple(color) {
    this.ripples.push({ radius: 12, life: 0.7, maxLife: 0.7, color: color || this.color });
  }

  arrivalBurst(msgType) {
    const color = ARRIVAL_COLORS[msgType] || ARRIVAL_COLORS.default;
    this.particles.emit("arrival", this.x, this.y, color);
    this.triggerRipple(color);
  }

  update(dt) {
    // Smooth position lerp
    this.x += (this.targetX - this.x) * 4 * dt;
    this.y += (this.targetY - this.y) * 4 * dt;

    // Spawn fade-in
    if (this.spawning) {
      this.spawnAlpha = Math.min(1, this.spawnAlpha + dt * 3);
      if (this.spawnAlpha >= 1) this.spawning = false;
    }

    // Animation speed: activity-driven, or fallback to task/blocked/sleeping
    const activitySpeed = this.activity ? (ACTIVITY_FRAME_SPEED[this.activity] || 0.5) : null;
    const animSpeed = this.sleeping ? 2.0 : activitySpeed || (this.isBlocked ? 1.0 : (this.currentTaskLabel ? 0.3 : 0.5));
    this.frameTimer += dt;
    if (this.frameTimer > animSpeed) {
      this.frameTimer = 0;
      this.frameIndex = (this.frameIndex + 1) % this.frames.length;
    }

    // Presence phases
    this.glowPhase += dt * 2;
    this.breathPhase += dt * (this.sleeping ? 0.6 : 1.5);
    if (this.sleeping) this._sleepPhase += dt;
    if (this.isBlocked) {
      this._blockedPhase += dt * 4;
      this._shakeX = Math.sin(this._blockedPhase * 8) * 2; // horizontal shake
    } else {
      this._shakeX *= 0.9; // decay shake
    }
    if (this.currentTaskLabel && !this.isBlocked) {
      this._workingPhase += dt * 3;
    }
    // Activity phase for ring animation (no particles — clean)
    if (this.activity && this.activity !== "idle") {
      this._activityPhase = (this._activityPhase || 0) + dt * 3;
    }

    // Ripples
    for (let i = this.ripples.length - 1; i >= 0; i--) {
      this.ripples[i].life -= dt;
      this.ripples[i].radius += dt * 90;
      if (this.ripples[i].life <= 0) this.ripples.splice(i, 1);
    }

    // Bubble
    if (this.bubble) {
      this.bubble.update(dt);
      if (!this.bubble.alive) this.bubble = null;
    }

    // Particles
    this.particles.update(dt);
  }

  render(ctx) {
    const alpha = this.spawning ? this.spawnAlpha : 1;
    const dimmed = this.highlighted === false && this._dimMode;

    // Breathing offset + blocked shake
    const breathY = Math.sin(this.breathPhase) * 2.5;
    const drawX = this.x + this._shakeX;
    const drawY = this.y + breathY;

    ctx.save();

    // --- Golden agent: outer luminescent aura ---
    if (this.isGolden && !dimmed) {
      const goldenPhase = this.glowPhase * 0.6;
      const goldenAlpha = 0.3 + 0.15 * Math.sin(goldenPhase);
      const goldenR = 44 + 4 * Math.sin(goldenPhase * 0.5);

      const goldenGrad = ctx.createRadialGradient(drawX, drawY, 10, drawX, drawY, goldenR);
      goldenGrad.addColorStop(0, `rgba(255, 215, 0, ${goldenAlpha * 0.7})`);
      goldenGrad.addColorStop(0.3, `rgba(255, 200, 0, ${goldenAlpha * 0.4})`);
      goldenGrad.addColorStop(0.6, `rgba(255, 170, 0, ${goldenAlpha * 0.15})`);
      goldenGrad.addColorStop(1, "rgba(255, 170, 0, 0)");
      ctx.fillStyle = goldenGrad;
      ctx.beginPath();
      ctx.arc(drawX, drawY, goldenR, 0, Math.PI * 2);
      ctx.fill();

      // Orbiting golden sparkles (5 particles)
      for (let i = 0; i < 5; i++) {
        const sa = goldenPhase * 1.5 + (i * Math.PI * 2 / 5);
        const sr = 34 + 3 * Math.sin(goldenPhase * 2 + i);
        const sx = drawX + Math.cos(sa) * sr;
        const sy = drawY + Math.sin(sa) * sr * 0.55;
        const sparkA = 0.5 + 0.45 * Math.sin(goldenPhase * 4 + i * 1.7);
        ctx.fillStyle = `rgba(255, 240, 120, ${sparkA})`;
        ctx.beginPath();
        ctx.arc(sx, sy, 1.8, 0, Math.PI * 2);
        ctx.fill();
      }
    }

    // --- Glow ring (online agents, subtle) ---
    if (this.online && !dimmed && !this.isGolden && !this.activity) {
      const basePulse = 0.12 + 0.06 * Math.sin(this.glowPhase);
      const hoverBoost = this.hovered ? 0.15 : 0;
      const intensity = basePulse + hoverBoost;
      const glowRadius = 34 + 2 * Math.sin(this.glowPhase * 0.7);

      const grad = ctx.createRadialGradient(drawX, drawY, 12, drawX, drawY, glowRadius);
      grad.addColorStop(0, this._rgba(this.color, intensity * 0.4));
      grad.addColorStop(0.6, this._rgba(this.color, intensity * 0.08));
      grad.addColorStop(1, this._rgba(this.color, 0));
      ctx.fillStyle = grad;
      ctx.beginPath();
      ctx.arc(drawX, drawY, glowRadius, 0, Math.PI * 2);
      ctx.fill();
    }

    // --- Ripples ---
    for (const ripple of this.ripples) {
      const progress = 1 - ripple.life / ripple.maxLife;
      const rippleAlpha = (1 - progress) * 0.5;
      ctx.globalAlpha = rippleAlpha * alpha;
      ctx.strokeStyle = ripple.color;
      ctx.lineWidth = 2 * (1 - progress);
      ctx.beginPath();
      ctx.arc(drawX, drawY, ripple.radius, 0, Math.PI * 2);
      ctx.stroke();
    }

    ctx.globalAlpha = dimmed ? alpha * 0.3 : alpha;

    // --- Working state: green tint underlay ---
    if (this.currentTaskLabel && !this.isBlocked && !dimmed) {
      const workAlpha = 0.08 + 0.04 * Math.sin(this._workingPhase);
      const workGrad = ctx.createRadialGradient(drawX, drawY, 8, drawX, drawY, 38);
      workGrad.addColorStop(0, `rgba(0, 230, 118, ${workAlpha})`);
      workGrad.addColorStop(1, "rgba(0, 230, 118, 0)");
      ctx.fillStyle = workGrad;
      ctx.beginPath();
      ctx.arc(drawX, drawY, 38, 0, Math.PI * 2);
      ctx.fill();
    }

    // --- Activity ring (thin, tight around sprite) ---
    if (this.activity && this.activity !== "idle" && !dimmed && !this.sleeping) {
      const glowColor = ACTIVITY_GLOW[this.activity];
      if (glowColor) {
        const phase = this._activityPhase || 0;
        const pulseAlpha = 0.3 + 0.2 * Math.sin(phase);
        ctx.strokeStyle = glowColor;
        ctx.globalAlpha = pulseAlpha;
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.arc(drawX, drawY, HALF + 8, 0, Math.PI * 2);
        ctx.stroke();
        ctx.globalAlpha = dimmed ? 0.3 : alpha;
      }
    }

    // --- Sprite (64x64) ---
    const frame = this.frames[this.frameIndex];
    if (frame) {
      if (this.sleeping) ctx.globalAlpha *= 0.5;
      ctx.drawImage(frame, drawX - HALF, drawY - HALF, SPRITE_SIZE, SPRITE_SIZE);
      if (this.sleeping) ctx.globalAlpha = dimmed ? 0.3 : alpha;
    }

    ctx.globalAlpha = dimmed ? 0.3 : 1;

    // --- Sleeping Zzz ---
    if (this.sleeping && !dimmed) {
      const t = this._sleepPhase;
      ctx.font = "bold 14px 'JetBrains Mono', monospace";
      ctx.textAlign = "center";
      for (let i = 0; i < 3; i++) {
        const phase = t * 0.8 + i * 0.7;
        const rise = (phase % 3) / 3; // 0→1 cycle
        const zx = drawX + 20 + i * 6;
        const zy = drawY - 20 - rise * 30;
        const za = (1 - rise) * 0.8;
        const size = 8 + i * 3;
        ctx.font = `bold ${size}px 'JetBrains Mono', monospace`;
        ctx.fillStyle = `rgba(150, 130, 255, ${za})`;
        ctx.fillText("z", zx, zy);
      }
    }

    // --- Blocked agent glow ---
    if (this.isBlocked && !dimmed) {
      const blockedPulse = 0.3 + 0.2 * Math.sin(this._blockedPhase);
      const blockedGrad = ctx.createRadialGradient(drawX, drawY, 10, drawX, drawY, 44);
      blockedGrad.addColorStop(0, `rgba(255,107,107,${blockedPulse})`);
      blockedGrad.addColorStop(1, `rgba(255,107,107,0)`);
      ctx.fillStyle = blockedGrad;
      ctx.beginPath();
      ctx.arc(drawX, drawY, 44, 0, Math.PI * 2);
      ctx.fill();
    }

    // --- Executive aura (luminescent golden halo) ---
    if (this.isExecutive && !this.isGolden && !dimmed) {
      const auraPhase = this.glowPhase * 0.8;
      const baseAlpha = 0.25 + 0.1 * Math.sin(auraPhase);
      const outerR = 40 + 3 * Math.sin(auraPhase * 0.7);
      const innerR = 20;

      const halo = ctx.createRadialGradient(drawX, drawY, innerR, drawX, drawY, outerR);
      halo.addColorStop(0, `rgba(255, 215, 0, ${baseAlpha * 0.5})`);
      halo.addColorStop(0.5, `rgba(255, 200, 0, ${baseAlpha * 0.2})`);
      halo.addColorStop(1, "rgba(255, 170, 0, 0)");
      ctx.fillStyle = halo;
      ctx.beginPath();
      ctx.arc(drawX, drawY, outerR, 0, Math.PI * 2);
      ctx.fill();

      for (let i = 0; i < 3; i++) {
        const sa = auraPhase * 1.2 + (i * Math.PI * 2 / 3);
        const sr = 32 + 2 * Math.sin(auraPhase * 2 + i);
        const sx = drawX + Math.cos(sa) * sr;
        const sy = drawY + Math.sin(sa) * sr * 0.6;
        const sparkA = 0.5 + 0.4 * Math.sin(auraPhase * 3 + i * 2);
        ctx.fillStyle = `rgba(255, 230, 100, ${sparkA})`;
        ctx.beginPath();
        ctx.arc(sx, sy, 1.5, 0, Math.PI * 2);
        ctx.fill();
      }
    }

    // --- Status dot (activity-aware) ---
    const dotX = drawX + 26;
    const dotY = drawY - 24;
    ctx.beginPath();
    ctx.arc(dotX, dotY, 4, 0, Math.PI * 2);
    const actColor = this.activity && this.activity !== "idle" ? ACTIVITY_GLOW[this.activity] : null;
    if (this.sleeping) {
      ctx.fillStyle = "#9b59b6";
      ctx.shadowColor = "#9b59b6";
      ctx.shadowBlur = 4;
    } else if (actColor) {
      ctx.fillStyle = actColor;
      ctx.shadowColor = actColor;
      ctx.shadowBlur = 5;
    } else if (this.online) {
      ctx.fillStyle = "#00e676";
      ctx.shadowColor = "#00e676";
      ctx.shadowBlur = 4;
    } else {
      ctx.fillStyle = "#636e72";
      ctx.shadowBlur = 0;
    }
    ctx.fill();
    ctx.shadowBlur = 0;

    // --- Name tag + team dots (always visible) ---
    const labelY = drawY + 40;
    const nameAlpha = this.hovered ? 1 : 0.85;
    ctx.font = "bold 12px 'JetBrains Mono', monospace";
    ctx.textAlign = "center";
    ctx.fillStyle = this.isGolden ? "#ffd700" : this.color;
    ctx.globalAlpha = (dimmed ? 0.3 : nameAlpha);

    const displayName = this.isGolden ? `★ ${this.name}` : this.name;
    ctx.fillText(displayName, drawX, labelY);

    // Team dots (small colored circles next to name)
    const TEAM_DOT_COLORS = { admin: "#ffd700", regular: "#00FFFF", bot: "#00FF88" };
    if (this._teams && this._teams.length > 0 && !dimmed) {
      const nameW = ctx.measureText(displayName).width;
      let dotStartX = drawX + nameW / 2 + 6;
      for (let i = 0; i < Math.min(this._teams.length, 3); i++) {
        const dotColor = TEAM_DOT_COLORS[this._teams[i].type] || TEAM_DOT_COLORS.regular;
        ctx.fillStyle = dotColor;
        ctx.globalAlpha = 0.7;
        ctx.beginPath();
        ctx.arc(dotStartX + i * 7, labelY - 4, 2.5, 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.globalAlpha = dimmed ? 0.3 : 1;
    }

    // --- Hover detail panel (role + activity + task) ---
    if (this.hovered && !dimmed) {
      let detailY = labelY + 13;
      ctx.textAlign = "center";

      // Role
      if (this.role) {
        ctx.font = "10px 'JetBrains Mono', monospace";
        ctx.fillStyle = "rgba(224,224,232,0.6)";
        ctx.globalAlpha = 1;
        const shortRole = this.role.length > 28 ? this.role.slice(0, 26) + "..." : this.role;
        ctx.fillText(shortRole, drawX, detailY);
        detailY += 12;
      }

      // Activity
      if (this.activity && this.activity !== "idle") {
        const actLabel = this.activityTool || this.activity;
        ctx.font = "9px 'JetBrains Mono', monospace";
        ctx.fillStyle = ACTIVITY_GLOW[this.activity] || "#888";
        ctx.globalAlpha = 0.7;
        ctx.fillText(actLabel, drawX, detailY);
        detailY += 11;
      }

      // Team names (compact list)
      if (this._teams && this._teams.length > 0) {
        ctx.font = "9px 'JetBrains Mono', monospace";
        const teamStr = this._teams.map(t => t.name).join(" · ");
        const shortTeams = teamStr.length > 35 ? teamStr.slice(0, 33) + "..." : teamStr;
        ctx.fillStyle = "rgba(0, 255, 255, 0.45)";
        ctx.globalAlpha = 1;
        ctx.fillText(shortTeams, drawX, detailY);
        detailY += 11;
      }

      // Project tag (only multi-project)
      if (this.showProjectTag && this.project) {
        ctx.font = "9px 'JetBrains Mono', monospace";
        ctx.fillStyle = "rgba(108, 92, 231, 0.5)";
        ctx.fillText(this.project, drawX, detailY);
        detailY += 11;
      }

      // Current task
      if (this.currentTaskLabel) {
        ctx.font = "9px 'JetBrains Mono', monospace";
        const taskLabel = this.currentTaskLabel.length > 30
          ? this.currentTaskLabel.slice(0, 28) + "..."
          : this.currentTaskLabel;
        ctx.fillStyle = this.isBlocked ? "rgba(255, 107, 107, 0.7)" : "rgba(0, 230, 118, 0.55)";
        ctx.fillText(taskLabel, drawX, detailY);
      }
    } else if (!dimmed && this.currentTaskLabel) {
      // Non-hovered: show task label only (compact, one line under name)
      ctx.font = "9px 'JetBrains Mono', monospace";
      ctx.textAlign = "center";
      const taskLabel = this.currentTaskLabel.length > 22
        ? this.currentTaskLabel.slice(0, 20) + "..."
        : this.currentTaskLabel;
      ctx.fillStyle = this.isBlocked ? "rgba(255, 107, 107, 0.5)" : "rgba(0, 230, 118, 0.4)";
      ctx.globalAlpha = 0.8;
      ctx.fillText(taskLabel, drawX, labelY + 12);
    }

    ctx.restore();

    // --- Bubble ---
    if (this.bubble && !dimmed) {
      this.bubble.render(ctx, drawX, drawY - 44);
    }

    // --- Particles ---
    this.particles.render(ctx);
  }

  _rgba(hex, a) {
    const r = parseInt(hex.slice(1, 3), 16);
    const g = parseInt(hex.slice(3, 5), 16);
    const b = parseInt(hex.slice(5, 7), 16);
    return `rgba(${r},${g},${b},${a})`;
  }

  set dimMode(v) { this._dimMode = v; }
  get dimMode() { return this._dimMode; }
}
