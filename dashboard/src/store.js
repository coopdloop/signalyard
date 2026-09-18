import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export const useSession = create(
  persist(
    (set) => ({
      token: null,
      email: null,
      role: null,
      login: ({ token, email, role }) => set({ token, email, role }),
      logout: () => set({ token: null, email: null, role: null }),
    }),
    { name: 'signalyard-session' }
  )
)
