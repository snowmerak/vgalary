#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { chmod, copyFile, mkdir, rename, rm } from "node:fs/promises";
import { existsSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { parseArgs } from "node:util";

const repositoryURL = "https://github.com/snowmerak/vgalary.git";

function run(command, args, cwd, capture = false) {
  const result = spawnSync(command, args, {
    cwd,
    stdio: capture ? "pipe" : "inherit",
    encoding: "utf8",
    windowsHide: true,
    env: { ...process.env, GIT_TERMINAL_PROMPT: "0" },
  });
  if (result.error) throw new Error(`${command}: ${result.error.message}`);
  if (result.status !== 0)
    throw new Error(`${command} exited with status ${result.status}`);
  return result.stdout?.trim();
}

export function defaultDirectory() {
  if (process.platform === "win32")
    return join(
      process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local"),
      "vgalary",
    );
  if (process.platform === "darwin")
    return join(
      homedir(),
      "Library",
      "Application Support",
      "vgalary",
      "install",
    );
  return join(
    process.env.XDG_DATA_HOME || join(homedir(), ".local", "share"),
    "vgalary",
  );
}

// repository is injectable for isolated installation checks; published CLI uses repositoryURL.
export async function install({
  directory = defaultDirectory(),
  update = false,
  repository = repositoryURL,
} = {}) {
  const [major, minor] = process.versions.node.split(".").map(Number);
  if (major < 22 || (major === 22 && minor < 13))
    throw new Error("Node.js 22.13+ is required");
  run("git", ["--version"]);
  run("go", ["version"]);
  const target = resolve(directory);
  const source = join(target, "source");
  if (existsSync(source)) {
    const git = (args) => run("git", ["-C", source, ...args], undefined, true);
    if (
      realpathSync(git(["rev-parse", "--show-toplevel"])) !==
      realpathSync(source)
    )
      throw new Error("Installation source must be its own Git checkout");
    if (git(["remote", "get-url", "origin"]) !== repository)
      throw new Error(
        "Existing checkout has a different repository; choose another --directory",
      );
    if (git(["status", "--porcelain"]))
      throw new Error(
        "Existing checkout contains local changes; preserve them or choose another --directory",
      );
    if (update) run("git", ["-C", source, "pull", "--ff-only"]);
  } else {
    await mkdir(target, { recursive: true });
    run("git", ["clone", "--depth", "1", "--", repository, source]);
  }
  const builder = join(source, "scripts", "deploy.mjs");
  if (!existsSync(builder))
    throw new Error(
      "Checkout has no scripts/deploy.mjs; the release must include the web UI installer",
    );
  run(process.execPath, [builder, "--build-only"], source);
  const executable = process.platform === "win32" ? "vgalary.exe" : "vgalary";
  const binary = join(target, "bin", executable);
  await mkdir(dirname(binary), { recursive: true });
  const temporary = `${binary}.${process.pid}.tmp`;
  try {
    await copyFile(join(source, ".deploy", executable), temporary);
    if (process.platform !== "win32") await chmod(temporary, 0o755);
    await rename(temporary, binary);
  } catch (error) {
    throw new Error(
      `Could not install executable; stop any running installed instance and retry: ${error.message}`,
    );
  } finally {
    await rm(temporary, { force: true });
  }
  console.log(`\nInstalled: ${binary}`);
  console.log("Start the local server:");
  console.log(
    process.platform === "win32"
      ? `& '${binary.replaceAll("'", "''")}'`
      : `'${binary.replaceAll("'", "'\\''")}'`,
  );
  console.log("Then open http://127.0.0.1:8181");
  console.log(
    "Indexing needs ffmpeg/ffprobe, llama.cpp and Qdrant. Configure them in the local app.",
  );
  return binary;
}

if (
  process.argv[1] &&
  pathToFileURL(resolve(process.argv[1])).href === import.meta.url
) {
  const { values } = parseArgs({
    options: {
      directory: { type: "string" },
      update: { type: "boolean", default: false },
      help: { type: "boolean", default: false },
    },
  });
  if (values.help) {
    console.log(
      `Usage: node install.mjs [--directory PATH] [--update]\n\nRequires Git, Node.js 22.13+, npm and Go 1.27.3+.\nClones ${repositoryURL}, builds locally, and installs a user-owned executable.\nDefault directory: ${defaultDirectory()}\n--update fast-forwards an existing clean checkout.\nDoes not start the app, change PATH, register a service or publish local media.`,
    );
  } else {
    install({ directory: values.directory, update: values.update }).catch(
      (error) => {
        console.error(`Installation failed: ${error.message}`);
        process.exitCode = 1;
      },
    );
  }
}
