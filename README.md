# Hatch

> Chaque PR éclot en preview live, instantanément.

Outil open-source de **PR preview deployments** self-hosted. Pour chaque Pull Request GitHub, Hatch déploie automatiquement une version live de l'app sur un sous-domaine isolé, commente la PR avec le lien, et nettoie tout quand la PR est mergée ou fermée.

**Statut** : en développement.

## Structure

- `PRODUCT.md` — fiche produit (problème, solution, audience, promesse)
- `landing/` — landing page Vite + React (en cours)
- `core/` — control plane (à venir)

## Stack prévue

- **Backend** : .NET (control plane)
- **Container runtime** : Docker via SDK
- **Reverse proxy** : Traefik
- **Orchestration** : Kubernetes (à terme)
- **DB** : PostgreSQL
- **Frontend** : React + Vite

## Auteur

Samy Abdelmalek — [samyabdelmalek.fr](https://samyabdelmalek.fr/)
