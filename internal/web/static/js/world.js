/**
 * WorldBackground — rendered without camera transform (fills viewport).
 * Retro-Futurism / Cyberpunk aesthetic:
 *   - Deep dark gradient (#0F0F23 base per design system)
 *   - Perspective grid with vanishing point
 *   - Subtle CRT scanlines overlay
 *   - Floating neon particles
 */
export class WorldBackground {
  constructor() {
    this.y = -Infinity; // Always behind everything
    this.isBackground = true;
    this._phase = 0;
    this._particles = [];
    // Pre-generate ambient particles
    for (let i = 0; i < 30; i++) {
      this._particles.push({
        x: Math.random(),
        y: Math.random(),
        size: 0.5 + Math.random() * 1.5,
        speed: 0.005 + Math.random() * 0.015,
        hue: [0, 180, 270, 300][Math.floor(Math.random() * 4)], // cyan, blue, purple, pink
        alpha: 0.1 + Math.random() * 0.3,
        drift: (Math.random() - 0.5) * 0.003,
      });
    }
  }

  update(dt) {
    this._phase += dt;
    for (const p of this._particles) {
      p.y -= p.speed * dt;
      p.x += p.drift * dt;
      if (p.y < -0.02) {
        p.y = 1.02;
        p.x = Math.random();
      }
      if (p.x < 0) p.x = 1;
      if (p.x > 1) p.x = 0;
    }
  }

  render(ctx, w, h) {
    // --- Deep cyberpunk gradient ---
    const grad = ctx.createLinearGradient(0, 0, 0, h);
    grad.addColorStop(0, "#0F0F23");
    grad.addColorStop(0.5, "#0a0a18");
    grad.addColorStop(1, "#0d0b1a");
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, w, h);

    // --- Perspective grid (vanishing point at center-top) ---
    const vpX = w / 2;
    const vpY = h * 0.15;
    ctx.save();

    // Horizontal lines with perspective fade
    const horizLines = 20;
    for (let i = 0; i < horizLines; i++) {
      const t = (i + 1) / horizLines;
      const y = vpY + (h - vpY) * (t * t); // quadratic for perspective
      const alpha = 0.015 + t * 0.04;
      ctx.strokeStyle = `rgba(108, 92, 231, ${alpha})`;
      ctx.lineWidth = 0.5 + t * 0.5;
      ctx.beginPath();
      ctx.moveTo(0, y);
      ctx.lineTo(w, y);
      ctx.stroke();
    }

    // Vertical lines converging to vanishing point
    const vertLines = 16;
    for (let i = 0; i <= vertLines; i++) {
      const t = i / vertLines;
      const bottomX = t * w;
      const alpha = 0.01 + 0.03 * (1 - Math.abs(t - 0.5) * 2);
      ctx.strokeStyle = `rgba(108, 92, 231, ${alpha})`;
      ctx.lineWidth = 0.5;
      ctx.beginPath();
      ctx.moveTo(vpX, vpY);
      ctx.lineTo(bottomX, h);
      ctx.stroke();
    }
    ctx.restore();

    // --- Ambient floating particles ---
    ctx.save();
    for (const p of this._particles) {
      const px = p.x * w;
      const py = p.y * h;
      const flicker = 0.7 + 0.3 * Math.sin(this._phase * 2 + p.x * 10);
      ctx.globalAlpha = p.alpha * flicker;
      ctx.fillStyle = `hsl(${p.hue}, 100%, 70%)`;
      ctx.beginPath();
      ctx.arc(px, py, p.size, 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.restore();

    // --- Subtle CRT scanlines ---
    ctx.save();
    ctx.globalAlpha = 0.03;
    for (let y = 0; y < h; y += 3) {
      ctx.fillStyle = "#000";
      ctx.fillRect(0, y, w, 1);
    }
    ctx.restore();
  }
}

/**
 * World — rendered with camera transform.
 * Draws cluster circles, project labels, and hierarchy links.
 */
export class World {
  constructor() {
    this.y = -99999; // Behind agents but after background
    this.isBackground = false;
    this.clusters = []; // [{project, cx, cy, radius}, ...]
    this.hierarchyLinks = []; // [{from: AgentView, to: AgentView}, ...]
  }

  update() {}

  render(ctx, w, h) {
    // Per-cluster: dashed boundary circle + project label (skip hidden clusters)
    for (const cluster of this.clusters) {
      if (cluster.hidden) continue;

      ctx.save();
      ctx.setLineDash([4, 8]);
      ctx.strokeStyle = "rgba(108, 92, 231, 0.10)";
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.arc(cluster.cx, cluster.cy, cluster.radius, 0, Math.PI * 2);
      ctx.stroke();
      ctx.restore();

      // Project name label above the cluster
      ctx.save();
      ctx.font = "bold 14px 'JetBrains Mono', monospace";
      ctx.fillStyle = "rgba(108, 92, 231, 0.4)";
      ctx.textAlign = "center";
      ctx.fillText(cluster.project.toUpperCase(), cluster.cx, cluster.cy - cluster.radius - 16);
      ctx.restore();
    }

    // Hierarchy lines between agents (manager -> report)
    if (this.hierarchyLinks.length > 0) {
      ctx.save();
      for (const link of this.hierarchyLinks) {
        const mx = link.from.x, my = link.from.y;
        const rx = link.to.x, ry = link.to.y;

        const midX = (mx + rx) / 2;
        const midY = (my + ry) / 2;
        const dx = rx - mx, dy = ry - my;
        const len = Math.sqrt(dx * dx + dy * dy);
        if (len < 1) continue;

        // Find cluster center for this link
        let anchorX = 0, anchorY = 0;
        if (this.clusters.length > 0) {
          const closest = this.clusters[0];
          anchorX = closest.cx;
          anchorY = closest.cy;
          for (const c of this.clusters) {
            const d1 = Math.hypot(mx - c.cx, my - c.cy);
            const d2 = Math.hypot(mx - anchorX, my - anchorY);
            if (d1 < d2) { anchorX = c.cx; anchorY = c.cy; }
          }
        }

        const perpX = -dy / len;
        const perpY = dx / len;
        const toCenterX = anchorX - midX;
        const toCenterY = anchorY - midY;
        const dot = perpX * toCenterX + perpY * toCenterY;
        const sign = dot > 0 ? 1 : -1;
        const bulge = Math.min(len * 0.2, 40);
        const cpx = midX + perpX * bulge * sign;
        const cpy = midY + perpY * bulge * sign;

        ctx.setLineDash([5, 5]);
        ctx.strokeStyle = "rgba(162, 155, 254, 0.35)";
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        ctx.moveTo(mx, my);
        ctx.quadraticCurveTo(cpx, cpy, rx, ry);
        ctx.stroke();

        // Arrow at report end
        const t = 0.92;
        const nearX = (1 - t) * (1 - t) * mx + 2 * (1 - t) * t * cpx + t * t * rx;
        const nearY = (1 - t) * (1 - t) * my + 2 * (1 - t) * t * cpy + t * t * ry;
        const arrAngle = Math.atan2(ry - nearY, rx - nearX);
        const arrLen = 8;
        ctx.setLineDash([]);
        ctx.fillStyle = "rgba(108, 92, 231, 0.5)";
        ctx.beginPath();
        ctx.moveTo(rx - Math.cos(arrAngle) * 24, ry - Math.sin(arrAngle) * 24);
        ctx.lineTo(
          rx - Math.cos(arrAngle) * 24 - Math.cos(arrAngle - 0.5) * arrLen,
          ry - Math.sin(arrAngle) * 24 - Math.sin(arrAngle - 0.5) * arrLen
        );
        ctx.lineTo(
          rx - Math.cos(arrAngle) * 24 - Math.cos(arrAngle + 0.5) * arrLen,
          ry - Math.sin(arrAngle) * 24 - Math.sin(arrAngle + 0.5) * arrLen
        );
        ctx.closePath();
        ctx.fill();
      }
      ctx.restore();
    }
  }
}
