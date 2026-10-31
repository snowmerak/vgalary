#!/usr/bin/env node
import { spawn, spawnSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { existsSync, realpathSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { delimiter, dirname, join } from "node:path";
import { parseArgs } from "node:util";
import { createServer } from "node:net";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const { values } = parseArgs({
  options: {
    domain: { type: "string" },
    listen: { type: "string", default: "127.0.0.1:8181" },
    "data-dir": { type: "string" },
    "password-env": { type: "string" },
    "build-only": { type: "boolean", default: false },
    update: { type: "boolean", default: false },
    help: { type: "boolean", default: false },
  },
});

if (values.help) {
  console.log(`Usage: node scripts/deploy.mjs [options]

  --domain HOST           Optional HTTPS hostname behind your own reverse proxy
  --listen HOST:PORT      Loopback app address (default: 127.0.0.1:8181)
  --data-dir PATH         Override the Go app's per-user data directory
  --password-env NAME     Optional local login; domain mode defaults to VGALARY_WEB_PASSWORD
  --build-only           Build the embedded Go executable, do not start it
  --update               First git pull --ff-only; requires a clean working tree

Runtime: local-only by default at http://127.0.0.1:8181.
With --domain, your own HTTPS reverse proxy forwards the domain to --listen.
Indexing additionally needs ffmpeg/ffprobe, llama.cpp and Qdrant.
The password value never appears in command arguments or build metadata.`);
  process.exit(0);
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    stdio: "inherit",
    windowsHide: true,
    ...options,
  });
  if (result.error)
    throw new Error(`${command} could not start: ${result.error.message}`);
  if (result.status !== 0)
    throw new Error(`${command} exited with status ${result.status}`);
}
function capture(command, args) {
  const result = spawnSync(command, args, {
    cwd: root,
    encoding: "utf8",
    windowsHide: true,
  });
  if (result.error || result.status !== 0)
    throw new Error(`${command} is required and must be available on PATH`);
  return result.stdout.trim();
}
function npm(args) {
  // Run npm's JavaScript CLI through this Node binary on every OS. This avoids
  // cmd.exe argument concatenation and handles checkout paths containing spaces.
  const candidates = [
    process.env.npm_execpath,
    join(dirname(process.execPath), "node_modules/npm/bin/npm-cli.js"),
  ];
  for (const directory of (process.env.PATH || "").split(delimiter)) {
    candidates.push(join(directory, "node_modules/npm/bin/npm-cli.js"));
    const executable = join(
      directory,
      process.platform === "win32" ? "npm.cmd" : "npm",
    );
    if (existsSync(executable)) {
      try {
        candidates.push(realpathSync(executable));
      } catch {
        /* Continue looking through PATH. */
      }
    }
  }
  const cli = candidates.find(
    (candidate) =>
      candidate && candidate.endsWith("npm-cli.js") && existsSync(candidate),
  );
  if (!cli)
    throw new Error(
      "npm CLI not found; install npm with Node.js and ensure it is on PATH",
    );
  run(process.execPath, [cli, ...args], { cwd: join(root, "web") });
}
async function assertPortFree(address) {
  await new Promise((resolve, reject) => {
    const listener = createServer();
    listener.once("error", () =>
      reject(
        new Error(
          `${values.listen} is already in use; stop the existing app or choose --listen`,
        ),
      ),
    );
    listener.listen(
      Number(address.port),
      address.hostname.replace(/^\[|\]$/g, ""),
      () => listener.close((error) => (error ? reject(error) : resolve())),
    );
  });
}

async function main() {
  const [major, minor] = process.versions.node.split(".").map(Number);
  if (major < 22 || (major === 22 && minor < 13))
    throw new Error("Node.js 22.13+ is required");
  if (
    values.domain &&
    (!/^(?=.{1,253}$)[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$/.test(
      values.domain,
    ) ||
      !values.domain.includes("."))
  )
    throw new Error("--domain must be a hostname, without https:// or a path");
  const passwordEnv = values["password-env"] || "VGALARY_WEB_PASSWORD";
  const requirePassword = !!(values.domain || values["password-env"]);
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(passwordEnv))
    throw new Error("Invalid password environment variable name");
  const address = new URL(`http://${values.listen}`);
  if (
    !["127.0.0.1", "[::1]", "localhost"].includes(address.hostname) ||
    !address.port ||
    address.pathname !== "/" ||
    address.username ||
    address.password ||
    address.search ||
    address.hash
  )
    throw new Error("--listen must be a loopback HOST:PORT");
  if (
    !values["build-only"] &&
    requirePassword &&
    (!process.env[passwordEnv] ||
      Buffer.byteLength(process.env[passwordEnv], "utf8") < 16)
  )
    throw new Error(
      `Set ${passwordEnv} to a password of at least 16 bytes before starting password-protected mode`,
    );
  if (!values["build-only"]) await assertPortFree(address);
  const gitVersion = capture("git", ["--version"]);
  const goVersion = capture("go", ["version"]);
  console.log(
    `Building vgalary: Node ${process.versions.node}, ${goVersion}, ${gitVersion}`,
  );
  if (values.update) {
    if (capture("git", ["status", "--porcelain"]))
      throw new Error("--update requires a clean working tree");
    run("git", ["pull", "--ff-only"]);
  }
  npm(["ci"]);
  npm(["run", "lint"]);
  npm(["test"]);
  npm(["run", "build"]);
  const output = join(root, ".deploy");
  await mkdir(output, { recursive: true });
  const executable = join(
    output,
    process.platform === "win32" ? "vgalary.exe" : "vgalary",
  );
  run("go", ["build", "-trimpath", "-o", executable, "./cmd/vgalary"]);
  let revision = "working-tree";
  try {
    revision = capture("git", ["rev-parse", "--short", "HEAD"]);
  } catch {
    /* Source archives may omit Git metadata. */
  }
  await writeFile(
    join(output, "build-info.json"),
    JSON.stringify(
      {
        revision,
        node: process.versions.node,
        go: goVersion,
        built_at: new Date().toISOString(),
      },
      null,
      2,
    ),
  );
  if (values.domain) {
    await writeFile(
      join(output, "Caddyfile.example"),
      `${values.domain} {\n    reverse_proxy ${values.listen}\n}\n`,
    );
  }
  console.log(`Built executable: ${executable}`);
  if (values["build-only"]) return;
  const args = ["-listen", values.listen];
  if (values.domain) args.push("-public-origin", `https://${values.domain}`);
  if (requirePassword) args.push("-web-password-env", passwordEnv);
  if (values["data-dir"]) args.push("-data-dir", values["data-dir"]);
  const child = spawn(executable, args, {
    cwd: root,
    stdio: "inherit",
    env: process.env,
    windowsHide: true,
  });
  let exited = false;
  const completion = new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => {
      exited = true;
      if (code === 0 || signal) resolve();
      else reject(new Error(`vgalary exited with status ${code}`));
    });
  });
  // A rejection before readiness must be observed immediately too.
  void completion.catch(() => {});
  const interrupt = () => {
    if (!exited) child.kill("SIGINT");
  };
  const terminate = () => {
    if (!exited) child.kill("SIGTERM");
  };
  process.once("SIGINT", interrupt);
  process.once("SIGTERM", terminate);
  try {
    let ready = false;
    for (let attempt = 0; attempt < 40 && !exited; attempt++) {
      try {
        const response = await fetch(`${address.origin}/api/health`, {
          signal: AbortSignal.timeout(1000),
        });
        const body = await response.json();
        if (response.ok && body.status === "ok") {
          ready = true;
          break;
        }
      } catch {
        /* Wait for the owned app to bind. */
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    if (!ready) {
      terminate();
      throw new Error("vgalary did not become ready within 10 seconds");
    }
    console.log(`Ready on ${address.origin}.`);
    if (values.domain)
      console.log(
        `Forward https://${values.domain} to ${values.listen} using your own HTTPS reverse proxy.`,
      );
    console.log(
      "Press Ctrl+C to stop. SQLite stays in the per-user app directory, or --data-dir.",
    );
    await completion;
  } finally {
    process.removeListener("SIGINT", interrupt);
    process.removeListener("SIGTERM", terminate);
    if (!exited) {
      terminate();
      await completion.catch(() => {});
    }
  }
}

main().catch((error) => {
  console.error(`Deployment failed: ${error.message}`);
  process.exitCode = 1;
});
