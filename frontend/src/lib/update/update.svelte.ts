import { Browser, Events } from "@wailsio/runtime";
import * as UpdateService from "../../../bindings/dev.jevido/work/services/updateservice.js";
import { UPDATE_AVAILABLE } from "../bridge/events";

/**
 * The `update:available` payload.
 *
 * Declared here rather than imported: the generated event table only learns
 * about an event once the Go side emits it, so until then `e.data` is `any`
 * and this is the only thing saying what shape it has.
 *
 * Field names follow the Go struct's json tags (`version`, `releaseUrl`), as
 * every other event in this app does.
 */
interface UpdateAvailablePayload {
  version?: string;
  releaseUrl?: string;
}

/**
 * A release newer than the one running.
 *
 * Deliberately session-only: nothing here is written to storage, so closing
 * the popup puts it away until the next launch rather than for good. An update
 * the user keeps declining is a question worth asking again -- the only thing
 * that stops it being asked is installing it.
 *
 * The version already declined this session is remembered, though. The check
 * can run more than once in a long-lived window, and re-opening a popup that
 * was just closed, over the same version, is the app arguing with the user.
 */
export class AppUpdate {
  /** The version on offer, e.g. "1.4.0". Empty until the check finds one. */
  version = $state("");

  /** Where the release notes are, or empty if the backend sent none. */
  releaseUrl = $state("");

  /** Whether the popup is on screen. */
  showing = $state(false);

  /** True from the moment Update now is pressed. */
  applying = $state(false);

  /**
   * True once the backend has taken the update.
   *
   * Separate from `applying` because the button cannot go back to saying
   * "Update now": the app is on its way out to restart, and offering to start
   * again is offering to do it twice.
   */
  applied = $state(false);

  /** Why the update did not install. */
  error = $state<string | null>(null);

  /** The version the user has already closed the popup on, this session. */
  #declined: string | null = null;

  /** Subscribes to the backend's update check. Wired once, for the app's life. */
  listen(): () => void {
    return Events.On(UPDATE_AVAILABLE, (e) => {
      const payload = (e.data ?? {}) as UpdateAvailablePayload;
      const version = payload.version ?? "";
      // A popup that cannot name a version says only "something is available",
      // which is not enough to act on. Nothing is shown for it.
      if (!version) return;
      // Already put away once. Still true, still on offer next launch.
      if (version === this.#declined) return;

      this.version = version;
      this.releaseUrl = payload.releaseUrl ?? "";
      this.showing = true;
    });
  }

  /** Puts the popup away for this session only. */
  dismiss(): void {
    this.#declined = this.version;
    this.showing = false;
  }

  /**
   * Installs the update.
   *
   * The popup stays open on the way through: the backend restarts the app to
   * finish, and a window that closed the moment you pressed the button would
   * leave nothing on screen to explain why the app is about to disappear. It
   * also stays open on failure, holding the reason.
   */
  async apply(): Promise<void> {
    if (this.applying || this.applied) return;
    this.applying = true;
    this.error = null;
    try {
      await UpdateService.ApplyUpdate();
      this.applied = true;
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
    } finally {
      this.applying = false;
    }
  }

  /**
   * Opens the release notes in the user's browser.
   *
   * Not an `<a href>`: this window is the app, and a link in it navigates the
   * app to the release page with no way back.
   */
  openReleaseNotes(): void {
    if (!this.releaseUrl) return;
    void Browser.OpenURL(this.releaseUrl);
  }
}
