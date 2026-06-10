import { useCallback, useEffect, useState } from "react";
import {
  type Spot,
  type User,
  confirmSpot,
  createSpot,
  forgotPassword,
  getMe,
  getStoredUser,
  login,
  logout,
  nearby,
  register,
  reviewSpot,
  updateSpot,
} from "./api";
import { getCurrentPosition } from "./location";

// Quality palette — keep in sync with the server's canonical map
// (internal/web/web.go qualityColors): 0 unrated, 1 basic, 2 good, 3 great.
const QUALITY_COLOR: Record<number, string> = {
  0: "#9ca3af",
  1: "#dc2626",
  2: "#f59e0b",
  3: "#16a34a",
};

function speedText(s: Spot): string {
  const parts: string[] = [];
  if (s.down_mbps != null) parts.push(`${s.down_mbps}↓`);
  if (s.up_mbps != null) parts.push(`${s.up_mbps}↑`);
  let t = parts.length ? `${parts.join("/")} Mbps` : "";
  if (s.ping_ms != null) t += `${t ? " · " : ""}${s.ping_ms} ms`;
  return t;
}

function humanizeAgo(ms: number | undefined): string {
  if (!ms) return "";
  const d = (Date.now() - ms) / 1000;
  if (d < 60) return "just now";
  if (d < 3600) return `${Math.floor(d / 60)} min ago`;
  if (d < 86400) return `${Math.floor(d / 3600)} h ago`;
  if (d < 86400 * 30) return `${Math.floor(d / 86400)} days ago`;
  return `${Math.floor(d / (86400 * 30))} months ago`;
}

type View = "nearby" | "add";

export function App() {
  const [user, setUser] = useState<User | null>(getStoredUser());
  const [view, setView] = useState<View>("nearby");
  const [spotsAdded, setSpotsAdded] = useState<number | null>(null);

  // How many spots the logged-in user has contributed (from /api/me, not a
  // list length). Refreshed on login and after each successful add.
  const refreshMe = useCallback(() => {
    getMe()
      .then((me) => setSpotsAdded(me.spots_added))
      .catch(() => {
        /* non-fatal: just leave the count hidden */
      });
  }, []);

  useEffect(() => {
    if (user) refreshMe();
    else setSpotsAdded(null);
  }, [user, refreshMe]);

  // Auto-logout when any API call reports the session is no longer valid.
  useEffect(() => {
    const onUnauthorized = () => {
      setUser(null);
      setView("nearby");
    };
    window.addEventListener("openwifipassmap:unauthorized", onUnauthorized);
    return () => window.removeEventListener("openwifipassmap:unauthorized", onUnauthorized);
  }, []);

  return (
    <div className="min-h-screen flex flex-col">
      <header className="navbar bg-base-100 shadow-sm">
        <div className="flex-1 px-2 text-xl font-bold">📶 OpenWifiPassMap</div>
        <div className="flex-none px-2">
          {user ? (
            <div className="flex items-center gap-2">
              <span className="badge badge-primary" data-testid="user-badge">
                {user.username}
              </span>
              {spotsAdded != null && (
                <span className="text-sm opacity-70" data-testid="spots-added">
                  {spotsAdded} {spotsAdded === 1 ? "WiFi" : "WiFis"} added
                </span>
              )}
              <button
                className="btn btn-sm btn-ghost"
                data-testid="logout-btn"
                onClick={async () => {
                  await logout();
                  setUser(null);
                  setView("nearby");
                }}
              >
                Log out
              </button>
            </div>
          ) : null}
        </div>
      </header>

      <div role="tablist" className="tabs tabs-boxed m-2">
        <button
          role="tab"
          className={`tab ${view === "nearby" ? "tab-active" : ""}`}
          data-testid="nearby-tab"
          onClick={() => setView("nearby")}
        >
          Nearby
        </button>
        <button
          role="tab"
          className={`tab ${view === "add" ? "tab-active" : ""}`}
          data-testid="add-tab"
          onClick={() => setView("add")}
        >
          Add spot
        </button>
      </div>

      <main className="flex-1 p-3 max-w-xl w-full mx-auto">
        {!user && <AuthPanel onAuthed={setUser} />}
        {view === "nearby" && <NearbyView user={user} />}
        {view === "add" && user && <AddSpotView onAdded={refreshMe} />}
        {view === "add" && !user && (
          <p className="alert alert-info mt-3" data-testid="login-required">
            Log in above to add a spot.
          </p>
        )}
      </main>
    </div>
  );
}

function AuthPanel({ onAuthed }: { onAuthed: (u: User) => void }) {
  const [mode, setMode] = useState<"login" | "register">("register");
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  // error and notice are mutually exclusive, so one nullable value covers both.
  const [feedback, setFeedback] = useState<{ kind: "error" | "notice"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const fail = (err: unknown) =>
    setFeedback({ kind: "error", text: err instanceof Error ? err.message : "Something went wrong" });

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setFeedback(null);
    setBusy(true);
    try {
      const u =
        mode === "register" ? await register(username, email, password) : await login(username, password);
      onAuthed(u);
    } catch (err) {
      fail(err);
    } finally {
      setBusy(false);
    }
  }

  async function forgot() {
    setFeedback(null);
    if (!email.trim()) {
      setFeedback({ kind: "error", text: "Enter your email above, then tap “Forgot password?”." });
      return;
    }
    setBusy(true);
    try {
      setFeedback({ kind: "notice", text: await forgotPassword(email) });
    } catch (err) {
      fail(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card bg-base-100 shadow-sm mb-3">
      <form className="card-body gap-2" onSubmit={submit}>
        <h2 className="card-title text-base">
          {mode === "register" ? "Create an account to contribute" : "Welcome back"}
        </h2>
        <input
          className="input input-bordered"
          placeholder="Username"
          data-testid="auth-username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
        <input
          className="input input-bordered"
          type="email"
          placeholder={mode === "register" ? "Email" : "Email (for password reset)"}
          data-testid="auth-email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <input
          className="input input-bordered"
          type="password"
          placeholder="Password (≥8 chars)"
          data-testid="auth-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {feedback && (
          <p
            className={feedback.kind === "error" ? "text-error text-sm" : "text-success text-sm"}
            data-testid={feedback.kind === "error" ? "auth-error" : "auth-notice"}
          >
            {feedback.text}
          </p>
        )}
        <button className="btn btn-primary" data-testid="auth-submit" disabled={busy}>
          {busy ? "…" : mode === "register" ? "Sign up" : "Log in"}
        </button>
        {mode === "login" && (
          <button
            type="button"
            className="btn btn-link btn-sm"
            data-testid="auth-forgot"
            onClick={forgot}
          >
            Forgot password?
          </button>
        )}
        <button
          type="button"
          className="btn btn-link btn-sm"
          data-testid="auth-toggle"
          onClick={() => {
            setMode(mode === "register" ? "login" : "register");
            setFeedback(null);
          }}
        >
          {mode === "register" ? "I already have an account" : "Create a new account"}
        </button>
      </form>
    </div>
  );
}

function NearbyView({ user }: { user: User | null }) {
  const [spots, setSpots] = useState<Spot[] | null>(null);
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);

  async function find() {
    setBusy(true);
    setStatus("Locating…");
    try {
      const { lat, lng } = await getCurrentPosition();
      setStatus("Loading nearby spots…");
      const res = await nearby(lat, lng, 25);
      setSpots(res.results);
      setStatus(res.count === 0 ? "No spots nearby yet — add the first one!" : `${res.count} spot(s) nearby`);
    } catch (err) {
      setStatus(err instanceof Error ? err.message : "Could not get location");
    } finally {
      setBusy(false);
    }
  }

  // Replaces a single spot in the list (keyed by id) so SpotCard can hand back
  // the server's updated stats after a confirm without a full re-fetch.
  function replaceSpot(updated: Spot) {
    setSpots((prev) => prev?.map((s) => (s.id === updated.id ? updated : s)) ?? prev);
  }

  return (
    <section>
      <button className="btn btn-primary w-full" data-testid="locate-btn" onClick={find} disabled={busy}>
        {busy ? "…" : "Find WiFi near me"}
      </button>
      <p className="text-sm opacity-60 my-2" data-testid="status">
        {status}
      </p>
      <ul className="space-y-2" data-testid="spot-list">
        {spots?.map((s) => (
          <SpotCard key={s.id} spot={s} user={user} onUpdated={replaceSpot} />
        ))}
      </ul>
    </section>
  );
}

function SpotCard({
  spot,
  user,
  onUpdated,
}: {
  spot: Spot;
  user: User | null;
  onUpdated: (s: Spot) => void;
}) {
  const [revealed, setRevealed] = useState(false);
  const [confirmError, setConfirmError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [showRate, setShowRate] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const dist = spot.distance_km != null ? `${spot.distance_km.toFixed(1)} km` : "";
  const isOwn = user != null && spot.created_by === user.id;

  async function handleConfirm() {
    setConfirmError("");
    setConfirming(true);
    try {
      const updated = await confirmSpot(spot.id);
      onUpdated(updated);
    } catch (err) {
      setConfirmError(err instanceof Error ? err.message : "Could not confirm");
    } finally {
      setConfirming(false);
    }
  }

  return (
    <li className="card bg-base-100 shadow-sm" data-testid="spot-card">
      <div className="card-body p-4 gap-2">
        <div className="flex justify-between items-start">
          <div>
            <h3 className="font-semibold" data-testid="spot-venue">
              {spot.venue_name || spot.essid}
            </h3>
            <p className="text-sm opacity-70" data-testid="spot-essid">
              {spot.essid} · {spot.auth_type}
            </p>
          </div>
          <span className="badge badge-ghost" data-testid="spot-distance">
            {dist}
          </span>
        </div>
        {(spot.quality > 0 || speedText(spot)) && (
          <div className="flex flex-wrap items-center gap-2">
            {spot.quality > 0 && (
              <span
                className="text-xs font-semibold"
                style={{ color: QUALITY_COLOR[spot.quality] ?? QUALITY_COLOR[0] }}
                data-testid="spot-quality"
              >
                {"★".repeat(spot.quality)}
                {"☆".repeat(3 - spot.quality)}
              </span>
            )}
            {spot.ratings_count > 0 && (
              <span className="text-xs opacity-60" data-testid="spot-ratings-count">
                ({spot.ratings_count})
              </span>
            )}
            {speedText(spot) && (
              <span className="badge badge-ghost badge-sm text-xs" data-testid="spot-speed">
                {speedText(spot)}
              </span>
            )}
          </div>
        )}
        {spot.password ? (
          <button
            className="btn btn-sm btn-outline w-fit"
            data-testid="reveal-password"
            onClick={() => setRevealed((v) => !v)}
          >
            {revealed ? (
              <span data-testid="spot-password" className="font-mono">
                {spot.password}
              </span>
            ) : (
              "Show password"
            )}
          </button>
        ) : (
          <span className="text-sm opacity-60">Open network</span>
        )}
        {spot.notes && <p className="text-sm opacity-70">{spot.notes}</p>}

        <div className="flex flex-wrap items-center gap-2 mt-1" data-testid="spot-confirm-row">
          {spot.last_confirmed_at ? (
            <span className="badge badge-success badge-sm" data-testid="spot-confirmation">
              ✓ Confirmed {humanizeAgo(spot.last_confirmed_at)} · {spot.confirmations_count}{" "}
              {spot.confirmations_count === 1 ? "person" : "people"}
            </span>
          ) : (
            <span className="text-xs opacity-60" data-testid="spot-confirmation-none">
              Not yet confirmed
            </span>
          )}
          {user && !isOwn &&
            (spot.confirmed_by_me ? (
              <button
                className="btn btn-xs btn-ghost"
                data-testid="confirm-again"
                onClick={handleConfirm}
                disabled={confirming}
              >
                You confirmed it · refresh
              </button>
            ) : (
              <button
                className="btn btn-xs btn-success"
                data-testid="confirm-works"
                onClick={handleConfirm}
                disabled={confirming}
              >
                {confirming ? "…" : "Confirm works"}
              </button>
            ))}
          {user && (
            <button
              className="btn btn-xs btn-outline btn-primary"
              data-testid="spot-rate"
              onClick={() => {
                setShowRate((v) => !v);
                setShowEdit(false);
              }}
            >
              ★ Rate
            </button>
          )}
          {isOwn && (
            <button
              className="btn btn-xs btn-ghost"
              data-testid="spot-edit"
              onClick={() => {
                setShowEdit((v) => !v);
                setShowRate(false);
              }}
            >
              ✏️ Edit
            </button>
          )}
        </div>
        {confirmError && (
          <p className="text-error text-xs" data-testid="confirm-error">
            {confirmError}
          </p>
        )}
        {showRate && (
          <RateForm
            spot={spot}
            onSaved={(s) => {
              setShowRate(false);
              onUpdated(s);
            }}
          />
        )}
        {showEdit && (
          <EditForm
            spot={spot}
            onSaved={(s) => {
              setShowEdit(false);
              onUpdated(s);
            }}
          />
        )}
      </div>
    </li>
  );
}

// RateForm lets any logged-in user rate / speed-test a spot (their own
// included — that just edits their initial rating).
function RateForm({ spot, onSaved }: { spot: Spot; onSaved: (s: Spot) => void }) {
  const [quality, setQuality] = useState(spot.my_rating ? String(spot.my_rating) : "");
  const [down, setDown] = useState("");
  const [up, setUp] = useState("");
  const [ping, setPing] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const opt = (v: string, parse: (x: string) => number): number | null => {
    const n = parse(v);
    return Number.isFinite(n) && n >= 0 ? n : null;
  };

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    const body = {
      quality: quality ? parseInt(quality, 10) : null,
      down_mbps: opt(down, parseFloat),
      up_mbps: opt(up, parseFloat),
      ping_ms: opt(ping, (v) => parseInt(v, 10)),
    };
    if (body.quality == null && body.down_mbps == null && body.up_mbps == null && body.ping_ms == null) {
      setError("Pick a star rating and/or enter a speed measurement.");
      return;
    }
    setBusy(true);
    try {
      onSaved(await reviewSpot(spot.id, body));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save review");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="space-y-2 border-t pt-2 mt-1" data-testid="rate-form" onSubmit={submit}>
      <select
        className="select select-bordered select-sm w-full"
        data-testid="rate-quality"
        value={quality}
        onChange={(e) => setQuality(e.target.value)}
      >
        <option value="">Quality: keep my current rating</option>
        <option value="1">★ Basic</option>
        <option value="2">★★ Good</option>
        <option value="3">★★★ Great</option>
      </select>
      <div className="grid grid-cols-3 gap-2">
        <input className="input input-bordered input-sm" placeholder="↓ Mbps" type="number" min="0" step="0.1"
               data-testid="rate-down" value={down} onChange={(e) => setDown(e.target.value)} />
        <input className="input input-bordered input-sm" placeholder="↑ Mbps" type="number" min="0" step="0.1"
               value={up} onChange={(e) => setUp(e.target.value)} />
        <input className="input input-bordered input-sm" placeholder="ping ms" type="number" min="0" step="1"
               value={ping} onChange={(e) => setPing(e.target.value)} />
      </div>
      {error && <p className="text-error text-xs" data-testid="rate-error">{error}</p>}
      <button className="btn btn-primary btn-sm w-full" data-testid="rate-save" disabled={busy}>
        {busy ? "…" : "Save review"}
      </button>
    </form>
  );
}

// EditForm updates a spot's facts (venue, network, password, notes). Rating
// and speed are community data — they go through RateForm instead.
function EditForm({ spot, onSaved }: { spot: Spot; onSaved: (s: Spot) => void }) {
  const [venue, setVenue] = useState(spot.venue_name);
  const [essid, setEssid] = useState(spot.essid);
  const [authType, setAuthType] = useState(spot.auth_type);
  const [password, setPassword] = useState(spot.password);
  const [notes, setNotes] = useState(spot.notes);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      onSaved(
        await updateSpot(spot.id, {
          venue_name: venue,
          essid,
          auth_type: authType,
          password: authType === "open" ? "" : password,
          notes,
          lat: spot.lat,
          lng: spot.lng,
        }),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save changes");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="space-y-2 border-t pt-2 mt-1" data-testid="edit-form" onSubmit={submit}>
      <input className="input input-bordered input-sm w-full" placeholder="Venue name"
             data-testid="edit-venue" value={venue} onChange={(e) => setVenue(e.target.value)} />
      <input className="input input-bordered input-sm w-full" placeholder="Network name (SSID)"
             data-testid="edit-essid" value={essid} onChange={(e) => setEssid(e.target.value)} />
      <div className="grid grid-cols-2 gap-2">
        <select className="select select-bordered select-sm" value={authType}
                onChange={(e) => setAuthType(e.target.value)}>
          <option value="wpa2">WPA2</option>
          <option value="wpa3">WPA3</option>
          <option value="wep">WEP</option>
          <option value="open">Open</option>
        </select>
        <input className="input input-bordered input-sm" placeholder="Password" disabled={authType === "open"}
               data-testid="edit-password" value={authType === "open" ? "" : password}
               onChange={(e) => setPassword(e.target.value)} />
      </div>
      <textarea className="textarea textarea-bordered textarea-sm w-full" placeholder="Notes"
                value={notes} onChange={(e) => setNotes(e.target.value)} />
      {error && <p className="text-error text-xs" data-testid="edit-error">{error}</p>}
      <button className="btn btn-primary btn-sm w-full" data-testid="edit-save" disabled={busy}>
        {busy ? "…" : "Save changes"}
      </button>
    </form>
  );
}

function AddSpotView({ onAdded }: { onAdded: () => void }) {
  const [venue, setVenue] = useState("");
  const [essid, setEssid] = useState("");
  const [password, setPassword] = useState("");
  const [authType, setAuthType] = useState("wpa2");
  const [notes, setNotes] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setStatus("Getting your location…");
    try {
      const { lat, lng } = await getCurrentPosition();
      await createSpot({
        venue_name: venue,
        essid,
        password,
        auth_type: authType,
        notes,
        lat,
        lng,
      });
      setStatus("Saved! Switch to Nearby to see it.");
      setVenue("");
      setEssid("");
      setPassword("");
      setNotes("");
      onAdded();
    } catch (err) {
      setStatus(err instanceof Error ? err.message : "Could not save spot");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="card bg-base-100 shadow-sm" onSubmit={submit}>
      <div className="card-body gap-2">
        <h2 className="card-title text-base">Add a public WiFi spot</h2>
        <p className="text-xs opacity-60">Uses your current location as the spot's position.</p>
        <input
          className="input input-bordered"
          placeholder="Venue name (e.g. Blue Bottle Coffee)"
          data-testid="add-venue"
          value={venue}
          onChange={(e) => setVenue(e.target.value)}
        />
        <input
          className="input input-bordered"
          placeholder="Network name (SSID) *"
          data-testid="add-essid"
          value={essid}
          onChange={(e) => setEssid(e.target.value)}
        />
        <input
          className="input input-bordered"
          placeholder="Password (blank for open networks)"
          data-testid="add-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <select
          className="select select-bordered"
          data-testid="add-authtype"
          value={authType}
          onChange={(e) => setAuthType(e.target.value)}
        >
          <option value="wpa2">WPA2</option>
          <option value="wpa3">WPA3</option>
          <option value="wep">WEP</option>
          <option value="open">Open</option>
        </select>
        <textarea
          className="textarea textarea-bordered"
          placeholder="Notes (e.g. ask the barista, the cat is friendly)"
          data-testid="add-notes"
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
        />
        <button className="btn btn-primary" data-testid="add-submit" disabled={busy}>
          {busy ? "…" : "Save spot"}
        </button>
        {status && (
          <p className="text-sm" data-testid="add-status">
            {status}
          </p>
        )}
      </div>
    </form>
  );
}
