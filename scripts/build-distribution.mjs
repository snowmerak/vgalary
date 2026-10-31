#!/usr/bin/env node
import { copyFile, mkdir, writeFile, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const root = fileURLToPath(new URL("../", import.meta.url));
const output = join(root, ".deploy", "pages");
await mkdir(output, { recursive: true });
await mkdir(join(output, "assets"), { recursive: true });
for (const name of [
  "index.html",
  "styles.css",
  "404.html",
  "_headers",
  "assets/main-demo.webp",
])
  await copyFile(join(root, "site", name), join(output, name));
for (const name of ["install.sh", "install.ps1"])
  await copyFile(join(root, "scripts", name), join(output, name));
await rm(join(output, "install.mjs"), { force: true });
await writeFile(join(output, "robots.txt"), "User-agent: *\nAllow: /\n");
console.log(`Cloudflare Pages static files: ${output}`);
