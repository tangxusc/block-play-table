<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref } from "vue";
import { Terminal } from "@xterm/xterm";

const props = defineProps<{
  checkUrl: string;
  socketUrl: string;
}>();

const state = ref<"idle" | "connecting" | "connected" | "error">("idle");
const error = ref("");
const host = ref<HTMLElement | null>(null);
let terminal: Terminal | null = null;
let socket: WebSocket | null = null;

async function connect() {
  state.value = "connecting";
  error.value = "";
  try {
    const check = await fetch(props.checkUrl);
    if (!check.ok) {
      throw new Error((await check.text()) || `Terminal check failed (${check.status})`);
    }
    await nextTick();
    terminal?.dispose();
    terminal = new Terminal({
      cursorBlink: true,
      convertEol: true,
      rows: 18,
      cols: 96,
      fontSize: 13,
      theme: { background: "#101827" },
    });
    if (host.value) terminal.open(host.value);
    socket = new WebSocket(props.socketUrl);
    socket.addEventListener("open", () => {
      state.value = "connected";
    });
    socket.addEventListener("message", (message) => {
      const event = JSON.parse(String(message.data));
      if (event.type === "output") terminal?.write(event.data);
      if (event.type === "error") {
        state.value = "error";
        error.value = event.data || "Terminal error";
      }
    });
    socket.addEventListener("close", () => {
      if (state.value === "connected") terminal?.writeln("\r\nDisconnected");
    });
    terminal?.onData((data) => {
      socket?.send(JSON.stringify({ type: "input", data }));
    });
  } catch (cause) {
    state.value = "error";
    error.value = cause instanceof Error ? cause.message : String(cause);
  }
}

onBeforeUnmount(() => {
  socket?.close();
  terminal?.dispose();
});
</script>

<template>
  <div>
    <a-space class="terminal-toolbar">
      <a-button
        type="primary"
        :loading="state === 'connecting'"
        @click="connect"
      >
        Connect worker terminal
      </a-button>
      <a-tag v-if="state === 'connected'" color="green">Connected</a-tag>
      <a-alert
        v-if="state === 'error'"
        type="error"
        show-icon
        :message="error || 'Terminal unavailable'"
      />
    </a-space>
    <div ref="host" class="terminal-frame" />
  </div>
</template>
