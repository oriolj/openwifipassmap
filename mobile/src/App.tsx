import { useState } from "react";
import {
  type Spot,
  type User,
  createSpot,
  getStoredUser,
  login,
  logout,
  nearby,
  register,
} from "./api";
import { getCurrentPosition } from "./location";

type View = "nearby" | "add";

export function App() {
  const [user, setUser] = useState<User | null>(getStoredUser());
  const [view, setView] = useState<View>("nearby");

  return (
    <div className="min-h-screen flex flex-col">
      <header className="navbar bg-base-100 shadow-sm">
        <div className="flex-1 px-2 text-xl font-bold">📶 WiFi Spots</div>
        <div className="flex-none px-2">
          {user ? (
            <div className="flex items-center gap-2">
              <span className="badge badge-primary" data-testid="user-badge">
                {user.username}
              </span>
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
        {view === "nearby" && <NearbyView />}
        {view === "add" && user && <AddSpotView />}
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
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const u = mode === "register" ? await register(username, password) : await login(username, password);
      onAuthed(u);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
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
          type="password"
          placeholder="Password (≥8 chars)"
          data-testid="auth-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error && (
          <p className="text-error text-sm" data-testid="auth-error">
            {error}
          </p>
        )}
        <button className="btn btn-primary" data-testid="auth-submit" disabled={busy}>
          {busy ? "…" : mode === "register" ? "Sign up" : "Log in"}
        </button>
        <button
          type="button"
          className="btn btn-link btn-sm"
          data-testid="auth-toggle"
          onClick={() => {
            setMode(mode === "register" ? "login" : "register");
            setError("");
          }}
        >
          {mode === "register" ? "I already have an account" : "Create a new account"}
        </button>
      </form>
    </div>
  );
}

function NearbyView() {
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
          <SpotCard key={s.id} spot={s} />
        ))}
      </ul>
    </section>
  );
}

function SpotCard({ spot }: { spot: Spot }) {
  const [revealed, setRevealed] = useState(false);
  const dist = spot.distance_km != null ? `${spot.distance_km.toFixed(1)} km` : "";
  return (
    <li className="card bg-base-100 shadow-sm" data-testid="spot-card">
      <div className="card-body p-4">
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
      </div>
    </li>
  );
}

function AddSpotView() {
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
