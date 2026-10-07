import { create } from 'zustand';

/** 「所有项目」弹框的全局开关。 */
export const useProjectsModal = create<{ open: boolean; openProjects: () => void; closeProjects: () => void }>((set) => ({
  open: false,
  openProjects: () => set({ open: true }),
  closeProjects: () => set({ open: false }),
}));

export const openProjects = () => useProjectsModal.getState().openProjects();
