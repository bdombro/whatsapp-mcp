// End-to-end MCP contract verification (2026-07-28 stdio + HTTP/OAuth smoke).
// Usage: bun scripts/test-mcp.ts <path/to/whatsapp-mcp> <comma-separated-tools>

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import readline from "node:readline";

const [executable, expected] = process.argv.slice(2);
assert(executable && expected, "Usage: test-mcp.ts <executable> <comma-separated-tools>");
const binPath = path.resolve(executable);
const expectedTools = expected.split(",").filter(Boolean);

if (!fs.existsSync(binPath)) {
  console.error(`Binary not found: ${binPath}`);
  process.exit(1);
}

function makeIsolatedEnv(tmpDir: string): Record<string, string | undefined> {
  return {
    ...process.env,
    BUGSINK_DSN: "",
    WHATSAPP_CLIENT_DATA_DIR: tmpDir,
  };
}

async function testStdio(tmpDir: string): Promise<void> {
  const proc = spawn(binPath, ["mcp"], {
    env: makeIsolatedEnv(tmpDir),
    stdio: ["pipe", "pipe", "pipe"],
  });

  let stderr = "";
  proc.stderr.on("data", (chunk) => {
    stderr += chunk.toString();
  });

  const rl = readline.createInterface({ input: proc.stdout });
  const pending = new Map<number, { resolve: (msg: any) => void }>();
  rl.on("line", (line) => {
    const trimmed = line.trim();
    if (!trimmed) return;
    let msg: any;
    try {
      msg = JSON.parse(trimmed);
    } catch {
      return;
    }
    if (msg.id !== undefined && pending.has(msg.id)) {
      const { resolve } = pending.get(msg.id)!;
      pending.delete(msg.id);
      resolve(msg);
    }
  });

  let nextId = 1;
  const rpc = (method: string, params: Record<string, unknown> = {}): Promise<any> =>
    new Promise((resolve, reject) => {
      const id = nextId++;
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`Timeout waiting for ${method} (stderr: ${stderr})`));
      }, 5000);
      pending.set(id, {
        resolve: (msg) => {
          clearTimeout(timer);
          resolve(msg);
        },
      });
      proc.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    });

  const meta = {
    "io.modelcontextprotocol/protocolVersion": "2026-07-28",
    "io.modelcontextprotocol/clientCapabilities": {},
  };

  try {
    const disc = await rpc("server/discover", { _meta: meta });
    assert.ok(disc.result, `Expected result from server/discover: ${JSON.stringify(disc)}`);
    assert.equal(disc.result.resultType, "complete");
    assert.ok(disc.result.supportedVersions?.includes("2026-07-28"));
    assert.equal(disc.result._meta?.["io.modelcontextprotocol/serverInfo"]?.name, "whatsapp-mcp");

    const listed = await rpc("tools/list", { _meta: meta });
    assert.ok(listed.result?.tools, `Expected tools array: ${JSON.stringify(listed)}`);
    const names = listed.result.tools.map((t: { name: string }) => t.name).sort();
    assert.deepEqual(names, [...expectedTools].sort());

    const legacy = await rpc("initialize", {
      protocolVersion: "2024-11-05",
      capabilities: {},
      clientInfo: { name: "legacy-test", version: "0.0.1" },
    });
    assert.equal(
      legacy.error?.code,
      -32601,
      `Expected legacy initialize to return -32601, got ${JSON.stringify(legacy)}`,
    );

    const statusRes = await rpc("tools/call", {
      name: "status",
      arguments: {},
      _meta: meta,
    });
    assert.ok(!statusRes.error, `status failed: ${JSON.stringify(statusRes)}`);
  } finally {
    rl.close();
    proc.stdin.end();
    await new Promise<void>((resolve) => {
      const timer = setTimeout(() => {
        proc.kill("SIGKILL");
        resolve();
      }, 2000);
      proc.once("exit", () => {
        clearTimeout(timer);
        resolve();
      });
    });
  }
  console.log(`PASS stdio (2026-07-28 server/discover, ${expectedTools.length} tools, legacy -32601)`);
}

async function testHttpOAuthSmoke(tmpDir: string): Promise<void> {
  const proc = spawn(binPath, ["http", "--host", "127.0.0.1", "--port", "0", "--allow-ephemeral-port"], {
    env: makeIsolatedEnv(tmpDir),
    stdio: ["ignore", "pipe", "pipe"],
  });

  let logs = "";
  const baseUrl = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(new Error(`Timed out waiting for HTTP listener URL. Logs:\n${logs}`));
    }, 5000);
    const onData = (chunk: Buffer) => {
      logs += chunk.toString();
      const match = logs.match(/http:\/\/127\.0\.0\.1:(\d+)\/mcp/);
      if (match) {
        clearTimeout(timer);
        resolve(`http://127.0.0.1:${match[1]}`);
      }
    };
    proc.stdout.on("data", onData);
    proc.stderr.on("data", onData);
    proc.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`HTTP server exited early (${code}). Logs:\n${logs}`));
    });
  });

  try {
    const unauth = await fetch(`${baseUrl}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "server/discover",
        params: {},
      }),
    });
    assert.equal(unauth.status, 401);
    const wwwAuth = unauth.headers.get("www-authenticate") || "";
    assert.ok(
      wwwAuth.includes(`${baseUrl}/.well-known/oauth-protected-resource`),
      `Missing resource_metadata in WWW-Authenticate: ${wwwAuth}`,
    );

    const prmResp = await fetch(`${baseUrl}/.well-known/oauth-protected-resource`);
    assert.equal(prmResp.status, 200);
    const prm: any = await prmResp.json();
    assert.equal(prm.resource, `${baseUrl}/mcp`);
    assert.ok(Array.isArray(prm.authorization_servers));
    assert.equal(prm.authorization_servers[0], baseUrl);

    const asResp = await fetch(`${baseUrl}/.well-known/oauth-authorization-server`);
    assert.equal(asResp.status, 200);
    const asMeta: any = await asResp.json();
    assert.equal(asMeta.issuer, baseUrl);
    assert.equal(asMeta.authorization_endpoint, `${baseUrl}/authorize`);
    assert.equal(asMeta.token_endpoint, `${baseUrl}/token`);
    assert.equal(asMeta.registration_endpoint, `${baseUrl}/register`);
    assert.ok(asMeta.code_challenge_methods_supported.includes("S256"));

    const dcrResp = await fetch(`${baseUrl}/register`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Origin: baseUrl,
      },
      body: JSON.stringify({
        client_name: "test-mcp-smoke",
        redirect_uris: ["http://127.0.0.1:49152/callback"],
      }),
    });
    assert.equal(dcrResp.status, 201);
    const dcr: any = await dcrResp.json();
    assert.ok(typeof dcr.client_id === "string" && dcr.client_id.length > 0);
  } finally {
    const exitCode = await new Promise<number>((resolve) => {
      const timer = setTimeout(() => {
        proc.kill("SIGKILL");
        resolve(-1);
      }, 3000);
      proc.once("exit", (code) => {
        clearTimeout(timer);
        resolve(code ?? 0);
      });
      proc.kill("SIGTERM");
    });
    assert.equal(exitCode, 0, `Expected graceful SIGTERM exit 0, got ${exitCode}`);
  }
  console.log("PASS http (401 + WWW-Authenticate, RFC 9728, RFC 8414, DCR, SIGTERM exit 0)");
}

async function testNonLoopbackRefused(tmpDir: string): Promise<void> {
  const proc = spawn(binPath, ["http", "--host", "0.0.0.0", "--port", "10008"], {
    env: makeIsolatedEnv(tmpDir),
    stdio: ["ignore", "pipe", "pipe"],
  });
  const code = await new Promise<number>((resolve) => {
    const timer = setTimeout(() => {
      proc.kill("SIGKILL");
      resolve(-1);
    }, 3000);
    proc.once("exit", (c) => {
      clearTimeout(timer);
      resolve(c ?? 1);
    });
  });
  assert.notEqual(code, 0, "Expected --host 0.0.0.0 to be refused");
  console.log("PASS http refusal (--host 0.0.0.0 rejected)");
}

const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "whatsapp-mcp-smoke-"));
try {
  await testStdio(tmpDir);
  await testHttpOAuthSmoke(tmpDir);
  await testNonLoopbackRefused(tmpDir);
} finally {
  fs.rmSync(tmpDir, { recursive: true, force: true });
}
