// End-to-end MCP contract verification (2026-07-28 HTTP/OAuth smoke).
// Usage: bun scripts/test-mcp.ts <path/to/whatsapp-mcp> <comma-separated-tools>

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import fs from "node:fs";
import http from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";

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

async function testHttpOAuthSmoke(tmpDir: string): Promise<void> {
  const proc = spawn(binPath, ["http", "--host", "127.0.0.1", "--port", "0"], {
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

async function freePort(): Promise<number> {
  return await new Promise<number>((resolve, reject) => {
    const srv = net.createServer();
    srv.once("error", reject);
    srv.listen(0, "0.0.0.0", () => {
      const port = (srv.address() as net.AddressInfo).port;
      srv.close(() => resolve(port));
    });
  });
}

async function stopServer(proc: ReturnType<typeof spawn>): Promise<number> {
  return await new Promise<number>((resolve) => {
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
}

// Default bind is 0.0.0.0; OAuth metadata must still advertise 127.0.0.1
async function testHttpDefaultBind(tmpDir: string): Promise<void> {
  const port = await freePort();
  const proc = spawn(binPath, ["http", "--port", String(port)], {
    env: makeIsolatedEnv(tmpDir),
    stdio: ["ignore", "pipe", "pipe"],
  });
  const baseUrl = `http://127.0.0.1:${port}`;

  let logs = "";
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(new Error(`Timed out waiting for HTTP listener. Logs:\n${logs}`));
    }, 5000);
    const onData = (chunk: Buffer) => {
      logs += chunk.toString();
      if (logs.includes(`:${port}/mcp`)) {
        clearTimeout(timer);
        resolve();
      }
    };
    proc.stdout!.on("data", onData);
    proc.stderr!.on("data", onData);
    proc.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`HTTP server exited early (${code}). Logs:\n${logs}`));
    });
  });

  try {
    const prmResp = await fetch(`${baseUrl}/.well-known/oauth-protected-resource`);
    assert.equal(prmResp.status, 200);
    const prm: any = await prmResp.json();
    assert.equal(prm.resource, `${baseUrl}/mcp`);
    assert.equal(prm.authorization_servers[0], baseUrl);

    const asResp = await fetch(`${baseUrl}/.well-known/oauth-authorization-server`);
    assert.equal(asResp.status, 200);
    const asMeta: any = await asResp.json();
    assert.equal(asMeta.issuer, baseUrl);

    // Reachable on a non-loopback interface, still advertising 127.0.0.1
    const lanIp = Object.values(os.networkInterfaces())
      .flat()
      .find((a) => a && a.family === "IPv4" && !a.internal)?.address;
    if (lanIp) {
      const lanResp = await fetch(`http://${lanIp}:${port}/.well-known/oauth-protected-resource`);
      assert.equal(lanResp.status, 200);
      const lanPrm: any = await lanResp.json();
      assert.equal(lanPrm.resource, `${baseUrl}/mcp`);

      // /mcp stays Bearer-protected; AS endpoints refuse non-loopback peers even with a spoofed Host
      const lanRequest = (method: string, urlPath: string, headers: Record<string, string> = {}, body = "") =>
        new Promise<number>((resolve, reject) => {
          const req = http.request({ host: lanIp, port, method, path: urlPath, headers }, (res) => {
            res.resume();
            res.on("end", () => resolve(res.statusCode ?? 0));
          });
          req.on("error", reject);
          req.end(body);
        });
      const jsonHeaders = { "Content-Type": "application/json", Accept: "application/json, text/event-stream" };
      assert.equal(
        await lanRequest("POST", "/mcp", jsonHeaders, '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}'),
        401,
      );
      const authorizeQuery =
        "/authorize?response_type=code&client_id=x&redirect_uri=http%3A%2F%2F127.0.0.1%3A49152%2Fcb" +
        "&code_challenge=abc&code_challenge_method=S256&state=s";
      const registerBody = '{"client_name":"lan","redirect_uris":["http://127.0.0.1:49152/cb"]}';
      for (const host of [undefined, `127.0.0.1:${port}`]) {
        const hostHeader: Record<string, string> = host ? { Host: host } : {};
        assert.equal(await lanRequest("GET", "/.well-known/oauth-authorization-server", hostHeader), 403);
        assert.equal(await lanRequest("POST", "/register", { ...hostHeader, "Content-Type": "application/json" }, registerBody), 403);
        assert.equal(await lanRequest("GET", authorizeQuery, hostHeader), 403);
      }
    } else {
      console.log("SKIP non-loopback reachability (no non-internal IPv4 interface)");
    }
  } finally {
    assert.equal(await stopServer(proc), 0);
  }
  console.log("PASS http default bind (0.0.0.0, metadata advertises 127.0.0.1, AS loopback-only)");
}

const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "whatsapp-mcp-smoke-"));
try {
  await testHttpOAuthSmoke(tmpDir);
  await testHttpDefaultBind(tmpDir);
} finally {
  fs.rmSync(tmpDir, { recursive: true, force: true });
}
