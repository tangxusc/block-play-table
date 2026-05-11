import { rmSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

await import("./stop-local.mjs");

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
rmSync(join(root, "data"), { recursive: true, force: true });
rmSync(join(root, "worker-data"), { recursive: true, force: true });
rmSync(join(root, ".local-run"), { recursive: true, force: true });
console.log("Local data cleaned.");
