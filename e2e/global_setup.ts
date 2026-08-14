import { execFileSync } from "node:child_process";
import {
  chmodSync,
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

export type E2ERuntimeState = {
  runtimeDir: string;
  workerBinary: string;
  codexBinary: string;
  claudeBinary: string;
  gitUrl: string;
  pidFile: string;
};

export default async function globalSetup() {
  const root = path.resolve(__dirname, "..");
  const runtimeDir = mkdtempSync(path.join(tmpdir(), "bpt-playwright-a2a-"));
  const binarySuffix = process.platform === "win32" ? ".exe" : "";
  const workerBinary = path.join(runtimeDir, `worker${binarySuffix}`);
  const codexBinary = path.join(runtimeDir, `fake-codex${binarySuffix}`);
  const claudeBinary = path.join(runtimeDir, `fake-claude${binarySuffix}`);
  const fakeBinary = path.join(runtimeDir, `fake-agent${binarySuffix}`);
  const pidFile = path.join(runtimeDir, "worker-pids.txt");
  const gitUrl = path.join(runtimeDir, "repository");
  const goBinary = process.env.GO || "go";
  const goEnv = {
    ...process.env,
    GOCACHE: process.env.GOCACHE || path.join(root, ".gocache"),
    GOPATH: process.env.GOPATH || path.join(root, ".gopath"),
    GOMODCACHE:
      process.env.GOMODCACHE || path.join(root, ".gopath", "pkg", "mod"),
  };

  mkdirSync(gitUrl, { recursive: true });
  execFileSync("git", ["init", "--initial-branch=main"], {
    cwd: gitUrl,
    stdio: "ignore",
  });
  execFileSync("git", ["config", "user.name", "Block Play Table E2E"], {
    cwd: gitUrl,
  });
  execFileSync("git", ["config", "user.email", "e2e@block-play-table.invalid"], {
    cwd: gitUrl,
  });
  writeFileSync(path.join(gitUrl, "README.md"), "# Playwright A2A fixture\n");
  execFileSync("git", ["add", "README.md"], { cwd: gitUrl });
  execFileSync("git", ["commit", "-m", "Initialize Playwright fixture"], {
    cwd: gitUrl,
    stdio: "ignore",
  });

  execFileSync(goBinary, ["build", "-o", workerBinary, "./worker/cmd/worker"], {
    cwd: root,
    env: goEnv,
    stdio: "inherit",
  });
  execFileSync(
    goBinary,
    ["build", "-o", fakeBinary, "./e2e/fixtures/fake_agent"],
    { cwd: root, env: goEnv, stdio: "inherit" },
  );
  copyFileSync(fakeBinary, codexBinary);
  copyFileSync(fakeBinary, claudeBinary);
  if (process.platform !== "win32") {
    chmodSync(workerBinary, 0o755);
    chmodSync(codexBinary, 0o755);
    chmodSync(claudeBinary, 0o755);
  }
  writeFileSync(pidFile, "");

  const state: E2ERuntimeState = {
    runtimeDir,
    workerBinary,
    codexBinary,
    claudeBinary,
    gitUrl,
    pidFile,
  };
  const statePath = path.join(runtimeDir, "state.json");
  writeFileSync(statePath, JSON.stringify(state));
  process.env.BPT_E2E_RUNTIME_STATE = statePath;
  process.env.BPT_E2E_GIT_URL = gitUrl;

  return async () => {
    let pids: number[] = [];
    try {
      pids = readFileSync(pidFile, "utf8")
        .split(/\s+/)
        .map((value) => Number(value))
        .filter((value) => Number.isInteger(value) && value > 1);
    } catch {
      pids = [];
    }
    for (const pid of pids) {
      try {
        process.kill(pid, "SIGTERM");
      } catch {
        // 进程已由测试主动关闭时无需处理。
      }
    }
    rmSync(runtimeDir, { recursive: true, force: true });
  };
}
