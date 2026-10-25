import { describe, expect, it } from "vitest"
import type { StatePoint } from "../types"
import { smoothStatePoints } from "./smoothStatePoints"

const base = Date.parse("2026-07-02T12:00:00Z")
const point = (minute: number, cpu: number | null): StatePoint => ({
  ts: new Date(base + minute * 60_000).toISOString(),
  cpuPercent: cpu,
  load1: 0.2, load5: 0.2, load15: 0.2,
  memoryUsedBytes: 1024, memoryTotalBytes: 4096,
  swapUsedBytes: null, swapTotalBytes: null,
  diskUsedBytes: 2048, diskTotalBytes: 8192,
  netInTotalBytes: 1, netOutTotalBytes: 1,
  netInSpeedBps: 100, netOutSpeedBps: 100,
  processCount: 80, tcpConnectionCount: 20, udpConnectionCount: 4,
  uptimeSeconds: 60,
})

describe("smoothStatePoints", () => {
  it("cuts a lone CPU spike like the latency chart smoothing does", () => {
    const points = Array.from({ length: 20 }, (_, minute) => point(minute, minute === 15 ? 95 : 5))
    const smoothed = smoothStatePoints(points)
    expect(points[15].cpuPercent).toBe(95)
    expect(smoothed[15].cpuPercent).toBeLessThan(10)
  })

  it("keeps missing samples missing and capacities untouched", () => {
    const points = Array.from({ length: 20 }, (_, minute) => point(minute, minute === 14 ? null : 5))
    const smoothed = smoothStatePoints(points)
    expect(smoothed[14].cpuPercent).toBeNull()
    expect(smoothed[14].swapUsedBytes).toBeNull()
    expect(smoothed.every((item) => item.memoryTotalBytes === 4096 && item.diskTotalBytes === 8192)).toBe(true)
  })
})
