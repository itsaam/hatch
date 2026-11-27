<div align="center">

<img src="assets/logo.png" alt="Hatch" width="96" />

# Hatch

Préviews auto-hébergées pour les pull requests GitHub. Chaque preview peut lancer son application, sa base de données et ses services associés dans un environnement isolé.

[Site](https://hatchpr.dev) · [Documentation](https://hatchpr.dev/docs/) · [GitHub App](https://github.com/apps/hatchpr) · [Dépôt de démo](https://github.com/itsaam/hatch-demo-node)

</div>

## Première preview

1. Installe la GitHub App sur un dépôt autorisé.
2. Ajoute un `.hatch.yml` à la racine du dépôt.

   ```yaml
   version: 1
   services:
     web:
       build: .
       port: 3000
       expose: true
   ```

3. Ouvre une PR et écris `@hatchpr preview` seul dans un commentaire. Hatch publie le lien sur la PR.

Par défaut, répète la commande après un push pour mettre la preview à jour. Configure `previews.trigger: auto` si tu veux déclencher les builds à chaque mise à jour. Fermer la PR libère les ressources de la preview.

Le [dépôt de démo](https://github.com/itsaam/hatch-demo-node) montre un exemple avec Node et PostgreSQL. La [CLI](cli/README.md) peut générer un `.hatch.yml` pour ton projet.

## Déployer Hatch

- [Kubernetes et Argo CD](deploy/kubernetes/README.md) pour l’installation, le builder distant, les secrets et la bascule depuis Compose.
- [Docker Compose](deploy/docker-compose.md) pour une installation sur un serveur Linux derrière Traefik. Voir aussi [la protection du socket Docker](security/README.md).

Les bases de données des previews sont temporaires. Hatch est en bêta. [Signaler un problème](https://github.com/itsaam/hatch/issues) · [Licence](LICENSE)
