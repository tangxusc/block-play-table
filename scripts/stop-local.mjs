import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, rmSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const runDir = join(root, ".local-run");
const statePath = join(runDir, "state.json");

if (!existsSync(statePath)) {
  console.log("Local stack is not running.");
} else {
  const state = JSON.parse(readFileSync(statePath, "utf8"));

  for (const item of [...state.processes].reverse()) {
    if (item.wslPidFile && existsSync(item.wslPidFile)) {
      const pid = readFileSync(item.wslPidFile, "utf8").trim();
      if (pid) {
        try {
          execFileSync("wsl", ["-d", state.wslDistro || "Ubuntu-24.04", "--", "kill", "-TERM", pid], {
            stdio: "ignore",
          });
        } catch {
          // Process may already be gone.
        }
      }
    }
    if (item.pid) {
      killTree(item.pid);
    }
  }

  rmSync(statePath, { force: true });
  console.log("Local stack stopped.");
}

function killTree(pid) {
  try {
    if (process.platform === "win32") {
      execFileSync("taskkill", ["/PID", String(pid), "/T", "/F"], { stdio: "ignore" });
    } else {
      try {
        process.kill(-pid, "SIGTERM");
      } catch {
        process.kill(pid, "SIGTERM");
      }
    }
  } catch {
    // Process may already be gone.
  }
}
