import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { api, ApiError, atLeast, onSessionLost, setCsrfToken, type Me, type Role } from "./api";

interface SessionValue {
  user: Me | undefined;
  /** Undefined until the initial /auth/me call settles, so the UI can avoid
   *  flashing the login screen at someone who is already signed in. */
  ready: boolean;
  can: (role: Role) => boolean;
  signIn: (username: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
  refresh: () => Promise<void>;
}

const SessionContext = createContext<SessionValue | undefined>(undefined);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<Me | undefined>(undefined);
  const [ready, setReady] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const me = await api.me();
      setCsrfToken(me.csrfToken);
      setUser(me);
    } catch (err) {
      // A 401 here is the normal "not signed in yet" case. Anything else is a
      // real fault, but the response is the same: show the login screen.
      if (!(err instanceof ApiError) || err.status !== 401) {
        console.error("could not read the current session", err);
      }
      setUser(undefined);
    } finally {
      setReady(true);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // One expired session should bounce the whole UI to the login screen rather
  // than leaving each polling panel to fail on its own.
  useEffect(() => onSessionLost(() => setUser(undefined)), []);

  const signIn = useCallback(async (username: string, password: string) => {
    const me = await api.login(username, password);
    setCsrfToken(me.csrfToken);
    setUser(me);
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      setUser(undefined);
    }
  }, []);

  const value = useMemo<SessionValue>(
    () => ({
      user,
      ready,
      can: (role: Role) => atLeast(user?.role, role),
      signIn,
      signOut,
      refresh,
    }),
    [user, ready, signIn, signOut, refresh],
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionValue {
  const ctx = useContext(SessionContext);
  if (!ctx) {
    throw new Error("useSession must be used inside a SessionProvider");
  }
  return ctx;
}
