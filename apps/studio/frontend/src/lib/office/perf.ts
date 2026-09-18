/**
 * Frame statistics for the optional performance overlay.
 *
 * The stats object is mutated in place and read by the UI on a slow timer, so
 * measuring costs nothing per frame and never triggers reactivity.
 */
export interface FrameStats {
  /** Smoothed frames per second. */
  fps: number;
  /** Smoothed time spent inside the draw call, in milliseconds. */
  frameMs: number;
  /** Worst draw time seen in the last second, in milliseconds. */
  peakMs: number;
  /** Agents drawn in the last frame. */
  agents: number;
  /**
   * Dirty rectangles the last frame drew, or 0 when it was a full repaint.
   *
   * Worth a slot on the overlay because the scene is drawn once per patch:
   * this is the multiplier on everything `frameMs` measures, and a frame that
   * has quietly stopped merging is the shape a regression here takes.
   */
  patches: number;
  /** Frames drawn since the loop started. */
  frames: number;
}

export function createStats(): FrameStats {
  return { fps: 0, frameMs: 0, peakMs: 0, agents: 0, patches: 0, frames: 0 };
}

/** Exponential smoothing factor. Low enough to be readable, high enough to react. */
const SMOOTHING = 0.1;

export class PerfSampler {
  readonly stats = createStats();

  private peakWindow = 0;
  private peakResetAt = 0;

  /** Records one rendered frame. dt and drawMs are in seconds and ms. */
  sample(dt: number, drawMs: number, agents: number, patches: number, now: number): void {
    const s = this.stats;
    s.frames++;
    s.agents = agents;
    s.patches = patches;

    const instantFps = dt > 0 ? 1 / dt : 0;
    s.fps = s.fps === 0 ? instantFps : s.fps + (instantFps - s.fps) * SMOOTHING;
    s.frameMs = s.frameMs === 0 ? drawMs : s.frameMs + (drawMs - s.frameMs) * SMOOTHING;

    if (drawMs > this.peakWindow) this.peakWindow = drawMs;
    if (now >= this.peakResetAt) {
      s.peakMs = this.peakWindow;
      this.peakWindow = 0;
      this.peakResetAt = now + 1000;
    }
  }

  reset(): void {
    const s = this.stats;
    s.fps = 0;
    s.frameMs = 0;
    s.peakMs = 0;
    s.patches = 0;
    s.frames = 0;
    this.peakWindow = 0;
    this.peakResetAt = 0;
  }
}
