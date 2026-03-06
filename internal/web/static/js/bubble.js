const BUBBLE_LIFETIME = 6;
const MAX_TEXT_LEN = 300;
const MAX_LINE_WIDTH = 260;
const LINE_HEIGHT = 18;
const FONT = "13px 'JetBrains Mono', monospace";
const TYPEWRITER_SPEED = 45; // chars per second

export class Bubble {
  constructor(text, type = "speech", pinned = false) {
    this.fullText = text.length > MAX_TEXT_LEN
      ? text.slice(0, MAX_TEXT_LEN - 3) + "..."
      : text;
    this.type = type; // "speech" | "thought"
    this.pinned = pinned;
    this.life = BUBBLE_LIFETIME;
    this.alpha = 0;
    this.lines = null;

    // Typewriter state
    this._revealedChars = 0;
    this._cursorBlink = 0;
    this._typingDone = false;
  }

  get alive() {
    if (this.pinned) return true;
    return this.life > 0;
  }

  update(dt) {
    if (!this.pinned) {
      this.life -= dt;
    }

    if (this.pinned) {
      this.alpha = Math.min(1, this.alpha + dt * 5);
    } else if (this.life > BUBBLE_LIFETIME - 0.3) {
      this.alpha = Math.min(1, this.alpha + dt * 5);
    } else if (this.life < 0.5) {
      this.alpha = Math.max(0, this.life / 0.5);
    } else {
      this.alpha = 1;
    }

    // Typewriter reveal
    if (!this._typingDone) {
      this._revealedChars += TYPEWRITER_SPEED * dt;
      if (this._revealedChars >= this.fullText.length) {
        this._revealedChars = this.fullText.length;
        this._typingDone = true;
      }
    }

    this._cursorBlink += dt;
  }

  _wrapText(ctx, text) {
    ctx.font = FONT;
    const words = text.split(/\s+/);
    const lines = [];
    let current = "";

    for (const word of words) {
      const test = current ? current + " " + word : word;
      if (ctx.measureText(test).width > MAX_LINE_WIDTH && current) {
        lines.push(current);
        current = word;
      } else {
        current = test;
      }
    }
    if (current) lines.push(current);

    if (lines.length > 8) {
      lines.length = 8;
      lines[7] = lines[7].slice(0, -3) + "...";
    }

    return lines;
  }

  render(ctx, x, y) {
    if (this.alpha <= 0) return;

    ctx.save();
    ctx.globalAlpha = this.alpha;
    ctx.font = FONT;

    // Use full text for sizing so bubble doesn't resize during typing
    if (!this.lines) {
      this.lines = this._wrapText(ctx, this.fullText);
    }

    const maxW = Math.max(...this.lines.map((l) => ctx.measureText(l).width));
    const padding = 12;
    const w = maxW + padding * 2;
    const h = this.lines.length * LINE_HEIGHT + padding * 2 - 4;
    const bx = x - w / 2;
    const by = y - h - 14;

    // Get revealed text for typewriter
    const revealed = this.fullText.slice(0, Math.floor(this._revealedChars));
    const revealedLines = this._wrapText(ctx, revealed || " ");

    if (this.type === "speech") {
      ctx.fillStyle = "#fff";
      ctx.strokeStyle = "#2d3436";
      ctx.lineWidth = 1.5;

      this._roundRect(ctx, bx, by, w, h, 8);
      ctx.fill();
      ctx.stroke();

      // Tail
      ctx.beginPath();
      ctx.moveTo(x - 6, by + h);
      ctx.lineTo(x, by + h + 10);
      ctx.lineTo(x + 6, by + h);
      ctx.fillStyle = "#fff";
      ctx.fill();
      ctx.stroke();

      ctx.fillStyle = "#2d3436";
    } else {
      ctx.fillStyle = "rgba(30, 30, 46, 0.94)";
      ctx.strokeStyle = "#6c5ce7";
      ctx.lineWidth = 1;

      this._roundRect(ctx, bx, by, w, h, 8);
      ctx.fill();
      ctx.stroke();

      // Thought circles
      ctx.beginPath();
      ctx.arc(x - 2, by + h + 6, 3.5, 0, Math.PI * 2);
      ctx.fill();
      ctx.stroke();
      ctx.beginPath();
      ctx.arc(x + 4, by + h + 12, 2, 0, Math.PI * 2);
      ctx.fill();
      ctx.stroke();

      ctx.fillStyle = "#e0e0e8";
    }

    // Render revealed lines (typewriter effect)
    for (let i = 0; i < revealedLines.length; i++) {
      ctx.fillText(revealedLines[i], bx + padding, by + padding + 12 + i * LINE_HEIGHT);
    }

    // Blinking cursor at end of last revealed line
    if (!this._typingDone && revealedLines.length > 0) {
      const lastIdx = revealedLines.length - 1;
      const cursorX = bx + padding + ctx.measureText(revealedLines[lastIdx]).width + 2;
      const cursorY = by + padding + 3 + lastIdx * LINE_HEIGHT;
      const cursorOn = Math.sin(this._cursorBlink * 6) > 0;
      if (cursorOn) {
        ctx.fillRect(cursorX, cursorY, 6, 11);
      }
    }

    ctx.restore();
  }

  _roundRect(ctx, x, y, w, h, r) {
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.lineTo(x + w - r, y);
    ctx.quadraticCurveTo(x + w, y, x + w, y + r);
    ctx.lineTo(x + w, y + h - r);
    ctx.quadraticCurveTo(x + w, y + h, x + w - r, y + h);
    ctx.lineTo(x + r, y + h);
    ctx.quadraticCurveTo(x, y + h, x, y + h - r);
    ctx.lineTo(x, y + r);
    ctx.quadraticCurveTo(x, y, x + r, y);
    ctx.closePath();
  }
}
