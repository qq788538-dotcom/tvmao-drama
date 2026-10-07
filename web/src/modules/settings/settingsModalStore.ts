import { create } from 'zustand';

export type SectionKey = 'general' | 'models' | 'diag';

interface SettingsModalState {
  open: boolean;
  tab: SectionKey;
  openSettings: (tab?: SectionKey) => void;
  closeSettings: () => void;
  setTab: (tab: SectionKey) => void;
}

/** 设置弹框的全局开关。任何地方想「去设置」都调 openSettings(tab?)，不再跳路由。 */
export const useSettingsModal = create<SettingsModalState>((set) => ({
  open: false,
  tab: 'general',
  openSettings: (tab) => set((s) => ({ open: true, tab: tab ?? s.tab })),
  closeSettings: () => set({ open: false }),
  setTab: (tab) => set({ tab }),
}));

export const openSettings = (tab?: SectionKey) => useSettingsModal.getState().openSettings(tab);
