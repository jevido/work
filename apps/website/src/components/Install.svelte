<script lang="ts">
  /**
   * Where to get the app, on the page that is a picture of it.
   *
   * Somebody reading a board here was sent a link by somebody running Work.
   * That is the whole audience, and it is the only audience that has a reason
   * to want the binary -- so this sits in the footer, under the board, rather
   * than at the top where it would sell a thing to somebody who came to read
   * one line.
   *
   * It points at `releases/latest/download`, which GitHub redirects to the
   * newest release. No version is named anywhere in here: a number in this
   * file is a number that goes stale on the next push to main, and the page
   * has no way to know it happened.
   */

  /** What this browser is running on, as far as the app has an answer for it. */
  type Target =
    | { kind: "linux"; arch: "amd64" | "arm64" }
    | { kind: "source"; os: "macOS" | "Windows" }
    | { kind: "unknown" };

  const BASE = "https://github.com/jevido/work/releases/latest/download";
  const SOURCE = "https://github.com/jevido/work#building-from-source";

  /**
   * The first guess, from the user-agent string.
   *
   * Synchronous, so the footer renders with a real answer rather than a
   * placeholder that swaps a moment later. It gets the OS right nearly always
   * and the architecture only when the string happens to carry it -- Chrome on
   * Linux says `x86_64` whatever the machine is, which is exactly the case
   * `refine()` below exists for.
   */
  function guess(): Target {
    const ua = navigator.userAgent;
    if (/Mac OS X|Macintosh/i.test(ua) && !/iPhone|iPad/i.test(ua))
      return { kind: "source", os: "macOS" };
    if (/Windows NT/i.test(ua)) return { kind: "source", os: "Windows" };
    if (/Linux|X11|CrOS/i.test(ua))
      return { kind: "linux", arch: /aarch64|arm64/i.test(ua) ? "arm64" : "amd64" };
    return { kind: "unknown" };
  }

  let target = $state<Target>(guess());

  /**
   * The better answer, when the browser will give one.
   *
   * `getHighEntropyValues` is Chromium-only and asks the user's permission in
   * no way at all for `architecture`, but it is also the only interface that
   * tells the truth about an arm64 laptop. Firefox rejects the call and there
   * is nothing to do about that: the guess above stands, and an x86 binary on
   * an arm machine fails loudly at the first run rather than quietly, so the
   * arm64 link is offered underneath regardless.
   */
  $effect(() => {
    const data = (navigator as any).userAgentData;
    if (!data?.getHighEntropyValues) return;
    let live = true;
    data
      .getHighEntropyValues(["architecture", "platform"])
      .then((hints: { architecture?: string; platform?: string }) => {
        if (!live) return;
        if (hints.platform && !/linux/i.test(hints.platform)) return;
        if (target.kind !== "linux") return;
        target = { kind: "linux", arch: /arm/i.test(hints.architecture ?? "") ? "arm64" : "amd64" };
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  });

  const other = $derived(target.kind === "linux" && target.arch === "amd64" ? "arm64" : "amd64");
  const label = $derived(
    target.kind === "linux" ? (target.arch === "amd64" ? "x86-64" : "arm64") : "",
  );
</script>

<section class="install" aria-labelledby="install-heading">
  <h3 id="install-heading">Run it yourself</h3>

  {#if target.kind === "source"}
    <!--
      There is no asset to offer here, and a button that downloads a Linux
      binary onto a Mac would be a button that wastes somebody's time and then
      leaves them with a file they cannot run. Releases are Linux only.
    -->
    <p>
      Work is a desktop app for the machine you already work on. Releases are Linux; on
      {target.os} it has to be
      <a href={SOURCE}>built from source</a>.
    </p>
  {:else}
    <p>
      This is a read-only picture of somebody's board. The app that writes one is a desktop
      app you run on your own machine.
    </p>

    <p class="get">
      <a class="button" href="{BASE}/work-linux-{target.kind === 'linux' ? target.arch : 'amd64'}">
        Download Work for Linux{label ? ` (${label})` : ""}
      </a>
      <span class="also">
        or <a href="{BASE}/work-linux-{other}">{other}</a> ·
        <a href="{BASE}/checksums.txt">checksums.txt</a> ·
        <a href={SOURCE}>build from source</a>
      </span>
    </p>

    <!--
      Open rather than hidden behind a summary. The binary on its own does not
      run: it arrives unexecutable, and it draws into a webview it does not
      carry. Somebody who downloads it and finds nothing happens has been sent
      away by a page that knew the missing step and folded it away.
    -->
    <details open>
      <summary>Then, in the directory you downloaded it to</summary>
      <pre><code>sha256sum --check --ignore-missing checksums.txt
install -Dm755 work-linux-{target.kind === "linux" ? target.arch : "amd64"} ~/.local/bin/work

# the webview it draws into, which the binary does not carry
sudo pacman -S gtk4 webkitgtk-6.0                 # Arch
sudo apt install libgtk-4-1 libwebkitgtk-6.0-4    # Debian/Ubuntu

cd ~/src/some-project && work</code></pre>
      <p class="note">
        <code>~/.local/bin</code> rather than <code>/usr/local/bin</code> on purpose: Work
        updates itself by replacing its own file, so it has to own the directory it sits in.
        It also needs a <code>claude</code> that is already signed in, and it runs agents in
        the directory you start it from — so start it from one you are happy for it to
        change. <a href="https://github.com/jevido/work#install">The rest is in the README.</a>
      </p>
    </details>
  {/if}
</section>

<style>
  .install {
    font-size: 13px;
    margin-top: 20px;
    padding-top: 16px;
    border-top: 1px solid var(--line);
  }

  h3 {
    margin: 0 0 6px;
    font-size: 13px;
    color: var(--text);
  }

  p {
    margin: 0 0 10px;
    max-width: 62ch;
  }

  .get {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 12px;
  }

  /*
   * A link, dressed as a button. It navigates -- GitHub answers the asset URL
   * with a redirect and a Content-Disposition -- so it stays an anchor: a
   * <button> here would lose middle-click, "copy link", and the shape a
   * screen reader announces for something that goes somewhere.
   */
  .button {
    padding: 8px 16px;
    border: 1px solid var(--accent);
    border-radius: 6px;
    background: var(--accent);
    color: var(--on-accent);
    font-weight: 600;
    text-decoration: none;
  }

  .button:hover {
    text-decoration: underline;
  }

  .also {
    color: var(--muted);
  }

  a {
    color: inherit;
  }

  summary {
    cursor: pointer;
    margin-bottom: 8px;
  }

  /* Wide lines a phone cannot fit scroll sideways rather than wrapping: a
     wrapped shell command is a command somebody pastes wrong. */
  pre {
    margin: 0 0 8px;
    padding: 10px 12px;
    overflow-x: auto;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--panel);
    color: var(--text);
    font-size: 12px;
    line-height: 1.6;
  }

  code {
    font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, monospace;
  }

  .note {
    margin: 0;
    line-height: 1.6;
  }

  .note code {
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--panel-2);
  }
</style>
