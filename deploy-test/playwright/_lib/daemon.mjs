// Transient daemon helper used by Phase 2+ playwright scripts that need
// a *second* eidos gate daemon to mint a real card URI for end-to-end
// admit-correspondent flows.
//
// Why this exists:
// - Phase 2 (Contacts) needs a fresh card URI to scan / add. The cheapest
//   way to mint a well-formed one is to spin up a second daemon in a
//   throwaway state-dir, ask it for `eidos gate card`, and tear it down.
// - We can't use the host's normal daemon (its card belongs to the operator)
//   and we don't want to depend on a peer reachable over the network.
//
// Lifecycle contract:
// - Caller passes the path to the eidos binary (or relies on $EIDOS_BIN).
// - `spawnDaemon` returns {handle, cardURI, dashboardURL, stateDir, pubkey}.
//   `handle.stop()` MUST be called from a `finally` block — even on
//   assertion failure — or the test leaks processes / temp dirs.
// - We pick ephemeral ports and write them into config.toml before the
//   daemon starts, so two transient daemons can coexist on the same host.
//
// The script speaks to the daemon over its dashboard HTTP port (we poll
// /static/htmx.min.js for readiness) since that's what the playwright
// caller will be doing anyway.

import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createConnection } from 'node:net';

/**
 * Resolve the eidos binary to use. Precedence:
 *   1. Explicit `binary` argument
 *   2. $EIDOS_BIN env var
 *   3. The string `eidos` (relies on PATH)
 */
export function resolveEidosBin(binary) {
  return binary || process.env.EIDOS_BIN || 'eidos';
}

/**
 * Pick a free TCP port on 127.0.0.1 by asking the kernel for one (bind 0,
 * read back port, release). Not race-free against another process grabbing
 * it between release and re-bind, but good enough for transient test daemons
 * — the daemon will just fail to start if it loses the race, and the caller
 * can retry.
 */
export async function pickFreePort() {
  const { createServer } = await import('node:net');
  return await new Promise((resolve, reject) => {
    const srv = createServer();
    srv.unref();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
  });
}

/**
 * Poll a TCP host:port until a connection succeeds or `timeoutMs` elapses.
 * Throws on timeout. Used to wait for the dashboard HTTP listener.
 */
export async function waitForTCP(host, port, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const ok = await new Promise((resolve) => {
      const s = createConnection({ host, port, timeout: 500 }, () => {
        s.end();
        resolve(true);
      });
      s.on('error', () => resolve(false));
      s.on('timeout', () => {
        s.destroy();
        resolve(false);
      });
    });
    if (ok) return;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`waitForTCP: ${host}:${port} did not accept within ${timeoutMs}ms`);
}

/**
 * Run a synchronous eidos subcommand against the given state-dir. Throws
 * with stdout+stderr captured if the exit code is non-zero. Returns
 * trimmed stdout.
 */
function runEidos(binary, stateDir, args, opts = {}) {
  const env = {
    ...process.env,
    EIDOS_GATE_HOME: stateDir,
    EIDOS_NO_UPDATE_CHECK: '1',
    CI: 'true',
  };
  const r = spawnSync(binary, args, {
    env,
    stdio: ['ignore', 'pipe', 'pipe'],
    encoding: 'utf8',
    timeout: opts.timeoutMs || 10000,
  });
  if (r.status !== 0) {
    const out = (r.stdout || '').trim();
    const err = (r.stderr || '').trim();
    throw new Error(
      `eidos ${args.join(' ')} exited ${r.status}\n  stdout: ${out}\n  stderr: ${err}`,
    );
  }
  return (r.stdout || '').trim();
}

/**
 * Spawn a transient `eidos gate daemon` in a fresh state-dir. The daemon
 * is configured for dashboard-only operation (no embedded relay) and
 * binds to ephemeral ports so it does not collide with the operator's
 * primary daemon.
 *
 * Returns:
 *   {
 *     stateDir,           // temp dir; removed on stop()
 *     pubkey,             // 64-hex from `eidos gate whoami`
 *     cardURI,            // mindgate://… from `eidos gate card`
 *     dashboardURL,       // http://127.0.0.1:<port>
 *     handle: { stop }    // stop() kills the daemon AND removes stateDir
 *   }
 *
 * @param {object} opts
 * @param {string} [opts.binary]  Path to eidos binary; falls back to $EIDOS_BIN or `eidos`.
 * @param {string} [opts.label]   Identity label; default "pw-transient".
 * @param {string} [opts.home]    Home relay URL (only used to satisfy `init`); default "ws://127.0.0.1:1/".
 * @param {number} [opts.readyTimeoutMs] How long to wait for the dashboard port; default 15000.
 */
export async function spawnDaemon(opts = {}) {
  const binary = resolveEidosBin(opts.binary);
  const label = opts.label || 'pw-transient';
  const home = opts.home || 'ws://127.0.0.1:1/';
  const readyTimeoutMs = opts.readyTimeoutMs || 15000;

  const stateDir = mkdtempSync(join(tmpdir(), 'eidos-pw-'));
  const dashboardPort = await pickFreePort();

  let child = null;
  let stopped = false;
  const stop = async () => {
    if (stopped) return;
    stopped = true;
    if (child && !child.killed) {
      try { child.kill('SIGTERM'); } catch { /* ignore */ }
      // Give the daemon up to 3s to exit cleanly, then SIGKILL.
      const exited = await Promise.race([
        new Promise((res) => child.once('exit', () => res(true))),
        new Promise((res) => setTimeout(() => res(false), 3000)),
      ]);
      if (!exited) {
        try { child.kill('SIGKILL'); } catch { /* ignore */ }
      }
    }
    try {
      rmSync(stateDir, { recursive: true, force: true });
    } catch { /* ignore */ }
  };

  try {
    // 1. init the state-dir. --home requires a ws:// URL but doesn't have
    //    to actually be reachable — the daemon won't dial outbound until
    //    asked, and the dashboard is fine with an unreachable home relay.
    runEidos(binary, stateDir, ['gate', 'init', '--label', label, '--home', home]);

    // 2. Pin the dashboard listen port to the one we just chose. We don't
    //    touch daemon.socket — it defaults to <stateDir>/sock which is
    //    already unique per state-dir.
    runEidos(binary, stateDir, [
      'gate', 'config', 'set', 'dashboard.listen', `127.0.0.1:${dashboardPort}`,
    ]);
    // Embedded relay off — we only need the dashboard surface.
    runEidos(binary, stateDir, ['gate', 'config', 'set', 'relay.enabled', 'false']);

    // 3. Spawn the daemon.
    child = spawn(binary, ['gate', 'daemon'], {
      env: {
        ...process.env,
        EIDOS_GATE_HOME: stateDir,
        EIDOS_NO_UPDATE_CHECK: '1',
        CI: 'true',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
      detached: false,
    });
    // Surface unexpected exits — but only as warnings; the wait-for-port
    // loop is the real readiness gate.
    child.on('exit', (code, signal) => {
      if (!stopped) {
        process.stderr.write(
          `spawnDaemon: transient daemon at ${stateDir} exited unexpectedly code=${code} signal=${signal}\n`,
        );
      }
    });

    // 4. Wait for the dashboard listener to accept connections.
    await waitForTCP('127.0.0.1', dashboardPort, readyTimeoutMs);

    // 5. Capture identity pieces via the CLI (talks to the daemon over IPC).
    //    `whoami` prints a multi-line summary; extract the Hex line. There
    //    is no --hex flag today, and adding one would be a feature creep
    //    for the test harness — parsing the existing output is fine.
    const whoamiOut = runEidos(binary, stateDir, ['gate', 'whoami']);
    const hexMatch = whoamiOut.match(/Hex:\s*([0-9a-f]{64})/i);
    if (!hexMatch) {
      throw new Error(`spawnDaemon: could not parse hex from whoami output:\n${whoamiOut}`);
    }
    const pubkey = hexMatch[1].toLowerCase();
    const cardURI = runEidos(binary, stateDir, ['gate', 'card']).trim();

    return {
      stateDir,
      pubkey,
      cardURI,
      dashboardURL: `http://127.0.0.1:${dashboardPort}`,
      handle: { stop },
    };
  } catch (err) {
    await stop();
    throw err;
  }
}
