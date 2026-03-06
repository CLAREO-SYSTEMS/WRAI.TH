/**
 * ShortcutManager — Keyboard shortcut handler for Agent Relay dashboard.
 * Tokyo neon night 8-bit game aesthetic.
 */

export class ShortcutManager {
  constructor() {
    /** @type {Map<string, {actionId: string, description: string, handler: Function|null}>} */
    this._shortcuts = new Map();
    this._listener = null;
    this._overlayEl = null;

    // Register defaults (handlers wired later via register())
    const defaults = [
      ['1',          'mode-canvas',  'Canvas mode'],
      ['2',          'mode-detail',  'Agent detail mode'],
      ['3',          'mode-kanban',  'Kanban mode'],
      ['Escape',     'close',        'Close / return to default'],
      ['/',          'search',       'Focus search'],
      ['n',          'new-task',     'New task'],
      ['b',          'broadcast',    'Broadcast message'],
      ['ArrowLeft',  'nav-prev',     'Previous agent'],
      ['ArrowRight', 'nav-next',     'Next agent'],
      ['Enter',      'select',       'Open detail'],
      ['+',          'scale-up',     'Font scale up'],
      ['=',          'scale-up',     'Font scale up'],
      ['-',          'scale-down',   'Font scale down'],
      ['?',          'help',         'Toggle help'],
    ];

    for (const [key, actionId, description] of defaults) {
      this._shortcuts.set(key, { actionId, description, handler: null });
    }
  }

  /**
   * Register (or replace) a shortcut.
   * @param {string} key — The KeyboardEvent.key value
   * @param {string} actionId
   * @param {string} description
   * @param {Function} handler
   */
  register(key, actionId, description, handler) {
    this._shortcuts.set(key, { actionId, description, handler });
  }

  /** Attach the global keydown listener. */
  start() {
    if (this._listener) return;
    this._listener = (e) => this._onKeyDown(e);
    document.addEventListener('keydown', this._listener);
  }

  /** Detach the global keydown listener and remove overlay if open. */
  stop() {
    if (this._listener) {
      document.removeEventListener('keydown', this._listener);
      this._listener = null;
    }
    this._removeOverlay();
  }

  /** Toggle the retro help overlay. */
  toggleHelp() {
    if (this._overlayEl) {
      this._removeOverlay();
    } else {
      this._showOverlay();
    }
  }

  // ---------------------------------------------------------------------------
  // Internal
  // ---------------------------------------------------------------------------

  /** @param {KeyboardEvent} e */
  _onKeyDown(e) {
    // Ignore when user is typing in form elements
    const tag = e.target.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || e.target.isContentEditable) {
      return;
    }

    // Ignore if modifier keys are held (except Shift for ?, +)
    if (e.ctrlKey || e.metaKey || e.altKey) return;

    const entry = this._shortcuts.get(e.key);
    if (!entry) return;

    // Help toggle — use registered handler if available, else internal
    if (entry.actionId === 'help') {
      e.preventDefault();
      if (entry.handler) {
        entry.handler(e);
      } else {
        this.toggleHelp();
      }
      return;
    }

    // Escape also dismisses overlay if open
    if (entry.actionId === 'close' && this._overlayEl) {
      e.preventDefault();
      this._removeOverlay();
      return;
    }

    if (entry.handler) {
      e.preventDefault();
      entry.handler(e);
    }
  }

  _removeOverlay() {
    if (this._overlayEl) {
      this._overlayEl.remove();
      this._overlayEl = null;
    }
  }

  _showOverlay() {
    if (this._overlayEl) return;

    const overlay = document.createElement('div');
    overlay.id = 'shortcut-help-overlay';

    // Gather unique actionIds to avoid duplicate display for +/= etc.
    const seen = new Set();
    const rows = [];
    for (const [key, { actionId, description }] of this._shortcuts) {
      // Merge + and = into one row
      if (seen.has(actionId)) {
        // Append the extra key to the existing row
        const existing = rows.find((r) => r.actionId === actionId);
        if (existing) existing.keys.push(key);
        continue;
      }
      seen.add(actionId);
      rows.push({ keys: [key], actionId, description });
    }

    const rowsHtml = rows
      .map((r) => {
        const keyCaps = r.keys
          .map((k) => {
            const display = k === 'ArrowLeft' ? '\u2190'
              : k === 'ArrowRight' ? '\u2192'
              : k === 'Escape' ? 'ESC'
              : k === 'Enter' ? '\u21B5'
              : k;
            return `<span class="sc-key">${display}</span>`;
          })
          .join(' ');
        return `<tr><td class="sc-keys">${keyCaps}</td><td class="sc-desc">${r.description}</td></tr>`;
      })
      .join('');

    overlay.innerHTML = `
      <div class="sc-panel">
        <h2 class="sc-title">SHORTCUTS</h2>
        <table class="sc-table">
          <tbody>${rowsHtml}</tbody>
        </table>
        <p class="sc-hint">Press <span class="sc-key">?</span> or <span class="sc-key">ESC</span> to close</p>
      </div>
    `;

    // Inject scoped styles once
    if (!document.getElementById('shortcut-help-styles')) {
      const style = document.createElement('style');
      style.id = 'shortcut-help-styles';
      style.textContent = `
        #shortcut-help-overlay {
          position: fixed;
          inset: 0;
          z-index: 100;
          display: flex;
          align-items: center;
          justify-content: center;
          background: rgba(10, 10, 18, 0.92);
          animation: sc-fadein 0.15s ease-out;
        }
        @keyframes sc-fadein {
          from { opacity: 0; }
          to   { opacity: 1; }
        }
        .sc-panel {
          background: #0e0e1a;
          border: 3px double #6c5ce7;
          box-shadow:
            0 0 12px rgba(108, 92, 231, 0.5),
            inset 0 0 12px rgba(108, 92, 231, 0.15);
          padding: 28px 36px;
          min-width: 340px;
          max-width: 480px;
          image-rendering: pixelated;
          font-family: 'JetBrains Mono', 'Fira Code', 'Courier New', monospace;
        }
        .sc-title {
          text-align: center;
          color: #6c5ce7;
          font-size: 20px;
          letter-spacing: 6px;
          margin: 0 0 20px 0;
          text-shadow: 0 0 8px rgba(108, 92, 231, 0.7);
        }
        .sc-table {
          width: 100%;
          border-collapse: collapse;
        }
        .sc-table tr {
          border-bottom: 1px solid rgba(108, 92, 231, 0.15);
        }
        .sc-table tr:last-child {
          border-bottom: none;
        }
        .sc-table td {
          padding: 6px 4px;
          vertical-align: middle;
        }
        .sc-keys {
          text-align: right;
          padding-right: 14px !important;
          white-space: nowrap;
        }
        .sc-desc {
          color: #b8b8d0;
          font-size: 13px;
        }
        .sc-key {
          display: inline-block;
          background: #1a1a2e;
          color: #a29bfe;
          border: 1px solid #6c5ce7;
          border-radius: 3px;
          padding: 2px 8px;
          font-size: 12px;
          font-family: 'JetBrains Mono', 'Fira Code', 'Courier New', monospace;
          min-width: 18px;
          text-align: center;
          box-shadow:
            0 2px 0 #3d2f8f,
            0 0 4px rgba(108, 92, 231, 0.3);
          text-shadow: 0 0 4px rgba(162, 155, 254, 0.5);
        }
        .sc-hint {
          text-align: center;
          color: #555;
          font-size: 11px;
          margin: 18px 0 0 0;
        }
        .sc-hint .sc-key {
          font-size: 10px;
          padding: 1px 5px;
        }
      `;
      document.head.appendChild(style);
    }

    document.body.appendChild(overlay);
    this._overlayEl = overlay;

    // Close on click outside panel
    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) this._removeOverlay();
    });
  }
}
