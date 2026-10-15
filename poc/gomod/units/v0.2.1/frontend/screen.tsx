import { api } from "@margince/frontend/api";
import { useT } from "@margince/frontend/app";
import { Card, SectionHeader } from "@margince/frontend/design-system";
import { useQuery } from "@tanstack/react-query";

// #/ext/my-extension: asks the unit's own route whether it answers.
export default function MyExtensionScreen() {
  const t = useT();
  const ping = useQuery({
    queryKey: ["ext", "my-extension", "ping"],
    queryFn: async () => {
      const { data, error } = await api.GET("/ext/my-extension/ping");
      if (error) throw error;
      return data;
    },
  });
  return (
    <div className="wrap narrow">
      <SectionHeader title={t("extMyExtension.title")} level={1} />
      <Card>
        <p>
          {ping.data?.ok
            ? t("extMyExtension.answered")
            : t("extMyExtension.waiting")}
        </p>
      </Card>
    </div>
  );
}
