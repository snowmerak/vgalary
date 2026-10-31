import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scripts = fileURLToPath(new URL("./", import.meta.url));
const repositoryURL = "https://github.com/snowmerak/vgalary.git";
const powerShell = process.platform === "win32" ? "powershell.exe" : "pwsh";
function git(cwd, ...args) {
  const result = spawnSync("git", args, {
    cwd,
    encoding: "utf8",
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr);
}
function commit(cwd, message) {
  git(cwd, "add", ".");
  git(
    cwd,
    "-c",
    "user.name=Installer Test",
    "-c",
    "user.email=installer-test@example.invalid",
    "commit",
    "-m",
    message,
  );
}
function builder(version) {
  return `import { mkdirSync, writeFileSync } from 'node:fs';
const dir = new URL('../.deploy/', import.meta.url);
mkdirSync(dir, { recursive: true });
for (const name of ['vgalary', 'vgalary.exe']) writeFileSync(new URL(name, dir), ${JSON.stringify(version)});`;
}
function shellPath(path) {
  return path
    .replaceAll("\\", "/")
    .replace(/^([A-Za-z]):/, (_, drive) => `/${drive.toLowerCase()}`);
}

for (const kind of ["sh", "powershell"]) {
  test(`${kind}: install, update, and preserve executable on failed build or local changes`, async (t) => {
    const command = kind === "sh" ? "sh" : powerShell;
    if (
      spawnSync(
        command,
        kind === "sh"
          ? ["-c", ":"]
          : ["-NoProfile", "-NonInteractive", "-Command", "exit 0"],
        { windowsHide: true },
      ).error
    ) {
      t.skip(`${command} is not installed`);
      return;
    }
    const temporary = await mkdtemp(join(tmpdir(), "vgalary native install "));
    t.after(async () => {
      assert.equal(dirname(resolve(temporary)), resolve(tmpdir()));
      assert.ok(basename(temporary).startsWith("vgalary native install "));
      await rm(temporary, { recursive: true, force: true });
    });
    const repository = join(temporary, "upstream");
    await mkdir(join(repository, "scripts"), { recursive: true });
    await writeFile(join(repository, ".gitignore"), ".deploy/\n");
    await writeFile(
      join(repository, "scripts", "deploy.mjs"),
      builder("version one"),
    );
    git(repository, "init");
    commit(repository, "Initial fixture");
    const directory = join(temporary, "installed with spaces");
    const binary = join(
      directory,
      "bin",
      kind === "sh" ? "vgalary" : "vgalary.exe",
    );
    const env = {
      ...process.env,
      GIT_CONFIG_COUNT: "1",
      GIT_CONFIG_KEY_0: `url.${repository.replaceAll("\\", "/")}.insteadOf`,
      GIT_CONFIG_VALUE_0: repositoryURL,
    };
    const invoke = (update = false) =>
      spawnSync(
        command,
        kind === "sh"
          ? [
              shellPath(join(scripts, "install.sh")),
              "--directory",
              shellPath(directory),
              ...(update ? ["--update"] : []),
            ]
          : [
              "-NoProfile",
              "-NonInteractive",
              "-File",
              join(scripts, "install.ps1"),
              "-Directory",
              directory,
              ...(update ? ["-Update"] : []),
            ],
        { env, encoding: "utf8", windowsHide: true },
      );
    const success = (result) =>
      assert.equal(result.status, 0, result.stdout + result.stderr);
    success(invoke());
    assert.equal(await readFile(binary, "utf8"), "version one");
    await writeFile(
      join(repository, "scripts", "deploy.mjs"),
      builder("version two"),
    );
    commit(repository, "Update fixture");
    success(invoke(true));
    assert.equal(await readFile(binary, "utf8"), "version two");
    const localChanges = join(directory, "source", "local-changes.txt");
    await writeFile(localChanges, "keep these changes");
    const dirty = invoke(true);
    assert.notEqual(dirty.status, 0);
    assert.match(dirty.stdout + dirty.stderr, /local changes/);
    assert.equal(await readFile(localChanges, "utf8"), "keep these changes");
    assert.equal(await readFile(binary, "utf8"), "version two");
    await rm(localChanges);
    await writeFile(
      join(repository, "scripts", "deploy.mjs"),
      "process.exit(7);",
    );
    commit(repository, "Failed build fixture");
    assert.notEqual(invoke(true).status, 0);
    assert.equal(await readFile(binary, "utf8"), "version two");
    git(
      join(directory, "source"),
      "remote",
      "set-url",
      "origin",
      "https://example.invalid/other.git",
    );
    const other = invoke(true);
    assert.notEqual(other.status, 0);
    assert.match(other.stdout + other.stderr, /different repository/);
    assert.equal(await readFile(binary, "utf8"), "version two");
  });
}
